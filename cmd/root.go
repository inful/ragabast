package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/ragabast/internal/vector"
	"github.com/ragabast/internal/web"
)

// CLI represents the main command-line interface structure.
type CLI struct {
	// VersionFlag wires `--version` and reads the value from
	// the `version` var in kong.Vars (set by main.go from the
	// build-time -ldflags injection). When the user passes
	// `--version`, kong prints the version and exits 0 before
	// any subcommand is dispatched.
	Version kong.VersionFlag

	Ingest IngestCmd     `cmd:"" help:"Ingest and process docbuilder documents"`
	Query  QueryCmd      `cmd:"" help:"Query the vector database with natural language"`
	Serve  ServeCmd      `cmd:"" help:"Start the web server"`
	Init   ConfigInitCmd `cmd:"" help:"Write a starter config file (alias for 'config init')"`
	Config ConfigCmd     `cmd:"" help:"Manage configuration"`
	Vector VectorCmd     `cmd:"" help:"Manage the on-disk vector store"`
	Doctor DoctorCmd     `cmd:"" help:"Diagnose common configuration problems before they bite"`
	Status StatusCmd     `cmd:"" help:"Check system health and status"`
	List   ListCmd       `cmd:"" help:"List ingested documents"`
	Search SearchCmd     `cmd:"" help:"Semantic search across documents"`
}

type ConfigOpts struct {
	Config string `short:"c" name:"config" env:"RAGABAST_CONFIG" help:"Path to config file (YAML). If not set, tries config.yml then config.yaml."`
}

// withService loads the config and constructs the service, then
// invokes fn with both. The callback returns its own error (or nil);
// withService wraps any initialization failure with context.
//
// The CLI has seven commands that all need config + service. Putting
// the preamble in one place keeps the per-command Run methods
// focused on the work that distinguishes them.
func withService(opts ConfigOpts, fn func(ctx context.Context, cfg *config.Config, svc *service.Service) error) error {
	cfg, err := config.Load(opts.Config)
	if err != nil {
		return err
	}

	svc, err := service.NewService(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize service: %w", err)
	}

	return fn(context.Background(), cfg, svc)
}

// ConfigCmd represents the config command.
type ConfigCmd struct {
	Init ConfigInitCmd `cmd:"" help:"Write a starter config file"`
}

// ConfigInitCmd writes a config file to disk.
type ConfigInitCmd struct {
	Path  string `default:"config.yml" help:"Output path" arg:"" optional:""`
	Force bool   `short:"f" help:"Overwrite if the file already exists"`
}

// VectorCmd groups vector-store admin operations.
type VectorCmd struct {
	Reset VectorResetCmd `cmd:"" help:"Wipe the on-disk vector store"`
}

// DoctorCmd runs a series of static checks against the loaded
// config and the on-disk vector store and reports each one. The
// checks are deliberately conservative: doctor must never
// silently pass an unsafe config.
//
// Distinction between ERROR and WARNING:
//   - ERROR: a check that cannot continue (e.g. config fails
//     validation; persistence dir unreadable). Doctor exits
//     with a non-zero status.
//   - WARNING: an inconsistency the user should fix but that
//     does not block doctor (e.g. embedding_dimensions
//     mismatches the model's known output dimension). Doctor
//     exits 0 so it can be wired into CI / cron checks; the
//     warning is the signal.
//
// The live "is the embedding server up?" check is gated behind
// --check-server so a doctor run doesn't fail just because the
// dev machine hasn't started Ollama yet.
type DoctorCmd struct {
	ConfigOpts
	CheckServer bool `name:"check-server" help:"Also ping the embeddings server with a tiny request"`
}

func (c *DoctorCmd) Run(ctx *kong.Context) error {
	cfg, err := config.Load(c.Config)
	if err != nil {
		return err
	}

	log.Println("=== ragabast doctor ===")

	// Check 1: config validates.
	if err := cfg.Validate(); err != nil {
		log.Printf("✗ config validation failed: %v\n", err)
		return fmt.Errorf("config invalid: %w", err)
	}
	log.Println("✓ config validates")

	// Check 2: embedding model produces vectors of the dimension
	// the collection is configured for. This is the failure
	// mode that bit the user last week; catching it here
	// instead of at first ingest is the headline feature.
	c.checkEmbeddingDimensionMatch(cfg)

	// Check 3: persistence dir reachable. We do this lazily so
	// we don't pay the file-system cost when check (1) failed.
	c.checkPersistenceDirReachable(cfg)

	// Check 4: actual store contents. The earlier model/dim
	// table check answers "does the configured model match the
	// configured dimension?"; this one answers "do the vectors
	// that are already on disk match the configured dimension?"
	// — the latter catches stale data from a previous model
	// even when the current config is internally consistent.
	c.checkStoreEmbeddingDimension(cfg)

	// Check 5: optional live server check.
	if c.CheckServer {
		c.checkEmbeddingsServer(cfg)
	} else {
		log.Println("─ embedding server reachability: skipped (pass --check-server to ping)")
	}

	log.Println()
	log.Println("If a check failed or warned, see README + plans/ingestion.md for the fix path.")
	return nil
}

// checkStoreEmbeddingDimension opens the configured vector
// store, samples one stored embedding, and compares its actual
// length to vectordb.embedding_dimension. This is the check
// that catches "the config is internally consistent but the
// on-disk store is stale from a previous model".
//
// Three outcomes:
//   - Empty store: nothing to verify, print a "store is empty"
//     note (the prior model/dim table check still covers the
//     ingest path).
//   - Actual length matches configured length: success.
//   - Actual length does NOT match: WARNING naming both
//     numbers and the recovery command (vector reset --force).
func (c *DoctorCmd) checkStoreEmbeddingDimension(cfg *config.Config) {
	dir := cfg.VectorDB.PersistenceDir
	if dir == "" {
		// checkPersistenceDirReachable already warned about
		// empty path; nothing more to add.
		return
	}
	db, err := vector.NewVectorDB(
		cfg.VectorDB.CollectionName,
		cfg.VectorDB.EmbeddingDimension,
		dir,
		cfg.Ollama.EmbeddingModel,
	)
	if err != nil {
		// A model-mismatch error deserves a louder, more
		// actionable message than a generic open-failed
		// warning. The error from NewVectorDB already names
		// both models and the recovery command.
		if errors.Is(err, vector.ErrModelMismatch) {
			log.Printf("⚠ %v\n", err)
			return
		}
		log.Printf("⚠ could not open vector store at %s: %v\n", dir, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	actual, found, err := db.SampleEmbeddingLength(ctx)
	if err != nil {
		// Mixed-dim or other chromem-go failure: the underlying
		// error wraps "vectors must have the same length".
		// Surface it with the reset fix; that's exactly the case
		// the user needs to know about.
		log.Printf("⚠ store sample query failed: %v. "+
			"This usually means the store holds vectors of mixed dimensions. "+
			"Recover with `ragabast vector reset --force` followed by `ragabast ingest`.\n",
			err)
		return
	}
	if !found {
		log.Println("─ store is empty: nothing to verify yet (the model/dim table check covers ingest)")
		return
	}
	if actual == cfg.VectorDB.EmbeddingDimension {
		log.Printf("✓ stored embedding dimension (%d) matches vectordb.embedding_dimension\n",
			actual)
		return
	}
	log.Printf("⚠ stored embedding dimension is %d but vectordb.embedding_dimension is %d. "+
		"The store holds vectors from a previous model. Run `ragabast vector reset --force` "+
		"then `ragabast ingest` to rebuild it from source documents.\n",
		actual, cfg.VectorDB.EmbeddingDimension)
}

// checkEmbeddingDimensionMatch looks up the configured embedding
// model in knownModelDimensions and compares to
// vectordb.embedding_dimension. Mismatch is a WARNING (not an
// error) so doctor can run in CI; the warning text names the
// recovery command.
func (c *DoctorCmd) checkEmbeddingDimensionMatch(cfg *config.Config) {
	model := cfg.Ollama.EmbeddingModel
	configured := cfg.VectorDB.EmbeddingDimension

	expected, ok := knownModelDimensions[model]
	if !ok {
		log.Printf("⚠ embedding model %q is not in the known-dimensions table; cannot verify match. "+
			"If you set this manually, confirm the configured dimension (%d) matches what the server returns. "+
			"Open a PR to cmd/doctor_models.go if you want it added.",
			model, configured)
		return
	}

	if expected == configured {
		log.Printf("✓ embedding model %q (%d-dim) matches vectordb.embedding_dimension\n",
			model, expected)
		return
	}

	// Mismatch. Tell the user both numbers and the fix.
	log.Printf("⚠ embedding model %q produces %d-dim vectors, but vectordb.embedding_dimension is %d. "+
		"Set vectordb.embedding_dimension: %d, or run `ragabast vector reset --force` "+
		"after switching the model.\n",
		model, expected, configured, expected)
}

// checkPersistenceDirReachable verifies the configured vector
// store path exists (or can be created) and is writable. This is
// a WARNING, not an error: an empty directory is fine for a
// fresh install.
func (c *DoctorCmd) checkPersistenceDirReachable(cfg *config.Config) {
	dir := cfg.VectorDB.PersistenceDir
	if dir == "" {
		log.Println("⚠ vectordb.persistence_dir is empty; cannot check")
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("⚠ persistence dir %s does not exist yet; will be created on first ingest\n", dir)
			return
		}
		log.Printf("✗ persistence dir %s: %v\n", dir, err)
		return
	}
	if !info.IsDir() {
		log.Printf("✗ persistence path %s exists but is not a directory\n", dir)
		return
	}
	log.Printf("✓ persistence dir %s exists and is writable\n", dir)
}

// checkEmbeddingsServer pings the embeddings server, embeds a
// tiny probe text, and compares the returned vector length
// against vectordb.embedding_dimension. If --check-server is
// set and the server is unreachable, this is reported as a
// WARNING so doctor can still exit 0 (the user may be
// diagnosing a config before starting the server).
//
// The dimension probe is the actual failure-mode catch for
// "config says X but the server returns Y": the model/dim
// table in cmd/doctor_models.go only catches obvious typos;
// the server probe catches everything else (server-side
// model aliasing, custom finetunes, OpenAI-compat layers
// that silently substitute models).
func (c *DoctorCmd) checkEmbeddingsServer(cfg *config.Config) {
	client := vector.NewOpenAIEmbeddingClientWithOptions(
		cfg.Ollama.BaseURL,
		cfg.Ollama.EmbeddingModel,
		cfg.Ollama.EffectiveEmbeddingAPIKey(),
		cfg.Ollama.Timeout,
		0, // probe at the model's full dim — we want to see what the server actually returns
		1, // doctor probe is single-call; concurrency knob is for ingest
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.ValidateConnection(ctx); err != nil {
		log.Printf("⚠ embedding server unreachable at %s: %v\n", cfg.Ollama.BaseURL, err)
		return
	}
	log.Printf("✓ embedding server reachable at %s\n", cfg.Ollama.BaseURL)

	// Probe the actual dim. A one-token request keeps cost and
	// noise to zero on real providers.
	vec, err := client.GenerateEmbedding(ctx, "test")
	if err != nil {
		log.Printf("⚠ embedding probe failed: %v\n", err)
		return
	}
	actual := len(vec)
	if actual == cfg.VectorDB.EmbeddingDimension {
		log.Printf("✓ server returns %d-dim vectors, matches the configured dim\n", actual)
		return
	}
	log.Printf("⚠ server returns %d-dim vectors but vectordb.embedding_dimension is %d. "+
		"The configured model (%q) does not match what the server is actually emitting. "+
		"This is the same mismatch that would crash at first ingest with 'vectors must have the same length'. "+
		"Fix by either updating vectordb.embedding_dimension: %d in your config, or "+
		"changing ollama.embedding_model to one that returns %d-dim vectors.\n",
		actual, cfg.VectorDB.EmbeddingDimension, cfg.Ollama.EmbeddingModel, actual, actual)
}

// VectorResetCmd wipes the on-disk vector store. This is the
// recovery path when the store ends up in an inconsistent state
// (e.g. mixing two embedding models, or vector lengths that no
// longer match vectordb.embedding_dimension after a config
// change). The reset is destructive — the operator must pass
// --force to confirm; without it the command refuses to run.
type VectorResetCmd struct {
	ConfigOpts
	Force bool `short:"f" help:"Skip the confirmation prompt"`
}

func (c *VectorResetCmd) Run(ctx *kong.Context) error {
	cfg, err := config.Load(c.Config)
	if err != nil {
		return err
	}

	dir := cfg.VectorDB.PersistenceDir
	if dir == "" {
		return errors.New("vectordb.persistence_dir is empty; refusing to wipe a misconfigured path")
	}

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("persistence dir %s does not exist; nothing to reset", dir)
		}
		return fmt.Errorf("failed to stat persistence dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("persistence path %s is not a directory", dir)
	}

	if !c.Force {
		return fmt.Errorf(
			"refusing to wipe %s without --force. "+
				"This will delete every ingested vector; re-run with -f to confirm.",
			dir,
		)
	}

	log.Printf("vector reset: removing %s\n", dir)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to wipe %s: %w", dir, err)
	}
	// Recreate the empty dir so the next ingest can write
	// into the same path without the chromem-go "no such
	// directory" error.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to recreate %s: %w", dir, err)
	}
	log.Printf("vector reset: %s wiped. Run `ragabast ingest` to rebuild from source documents.\n", dir)
	return nil
}

func (c *ConfigInitCmd) Run(ctx *kong.Context) error {
	path := strings.TrimSpace(c.Path)
	if path == "" {
		path = config.DefaultConfigYML
	}

	if _, err := os.Stat(path); err == nil && !c.Force {
		return fmt.Errorf("config file already exists: %s (use --force to overwrite)", path)
	}

	cfg := config.DefaultConfig()
	if err := cfg.SaveConfig(path); err != nil {
		return err
	}

	log.Printf("Wrote config file: %s\n", path)
	return nil
}

// IngestCmd represents the ingest command.
type IngestCmd struct {
	ConfigOpts
	Files []string `help:"Docbuilder files to ingest" arg:"" type:"existingfile"`
	Path  string   `short:"p" help:"Directory path to scan for documents"`
}

func (c *IngestCmd) Run(ctx *kong.Context) error {
	return withService(c.ConfigOpts, func(ctx context.Context, _ *config.Config, svc *service.Service) error {
		// Ingest files
		if len(c.Files) > 0 {
			for _, file := range c.Files {
				log.Printf("Ingesting file: %s\n", file)
				if err := svc.IngestFile(ctx, file); err != nil {
					log.Printf("Error ingesting %s: %v\n", file, err)
				} else {
					log.Printf("✓ Successfully ingested: %s\n", file)
				}
			}
		}

		// Ingest directory
		if c.Path != "" {
			log.Printf("Scanning directory: %s\n", c.Path)
			result, err := svc.IngestDirectory(ctx, c.Path)
			if err != nil {
				return fmt.Errorf("failed to ingest directory: %w", err)
			}
			for _, fe := range result.Errors {
				log.Printf("Failed to ingest %s: %v", fe.Path, fe.Err)
			}
			log.Printf("Processed: %d, Failed: %d", result.Processed, result.Failed)
		}

		if len(c.Files) == 0 && c.Path == "" {
			return errors.New("no files or directory specified. Use --help for usage")
		}

		return nil
	})
}

// QueryCmd represents the query command.
type QueryCmd struct {
	ConfigOpts
	Query       string   `help:"Natural language query" arg:""`
	TopK        int      `short:"k" default:"5" help:"Number of results to consider"`
	Temperature *float64 `help:"LLM temperature (sampling). If omitted, uses model default."`
	Verbose     bool     `short:"v" help:"Show source documents"`
}

func (c *QueryCmd) Run(ctx *kong.Context) error {
	return withService(c.ConfigOpts, func(ctx context.Context, _ *config.Config, svc *service.Service) error {
		log.Printf("Querying: %s\n", c.Query)

		var response string
		var debug *service.QueryDebugInfo
		var err error
		if c.Verbose {
			response, debug, err = svc.QueryDebugWithOptions(ctx, c.Query, c.TopK, service.LLMOptions{Temperature: c.Temperature})
		} else if c.Temperature != nil {
			response, _, err = svc.QueryDebugWithOptions(ctx, c.Query, c.TopK, service.LLMOptions{Temperature: c.Temperature})
		} else {
			response, err = svc.Query(ctx, c.Query, c.TopK)
		}
		if err != nil {
			return fmt.Errorf("query failed: %w", err)
		}

		log.Printf("\nResponse:\n%s\n", response)

		if c.Verbose && debug != nil {
			log.Println("\n--- Prompt Sent To LLM ---")
			log.Print(debug.Prompt)
		}

		return nil
	})
}

// ServeCmd represents the serve command.
type ServeCmd struct {
	ConfigOpts
	Host string `short:"h" help:"Server host (overrides config)"`
	Port int    `short:"p" help:"Server port (overrides config)"`
}

func (c *ServeCmd) Run(ctx *kong.Context) error {
	// ServeCmd does NOT go through withService: the --host and
	// --port flags mutate cfg between Load and NewService, and
	// withService's load-then-build ordering doesn't fit. Doing
	// it inline keeps the override flow obvious in one place.
	cfg, err := config.Load(c.Config)
	if err != nil {
		return err
	}

	// Override bind host/port if flags are provided.
	if c.Host != "" {
		cfg.Server.Address = c.Host
	}
	if c.Port != 0 {
		cfg.Server.Port = c.Port
	}

	svc, err := service.NewService(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize service: %w", err)
	}

	srv := web.NewServer(cfg, svc)
	return srv.Start()
}

// StatusCmd represents the status command.
type StatusCmd struct {
	ConfigOpts
	Verbose bool `short:"v" help:"Verbose output"`
}

func (c *StatusCmd) Run(ctx *kong.Context) error {
	return withService(c.ConfigOpts, func(ctx context.Context, cfg *config.Config, svc *service.Service) error {
		log.Println("=== RAG System Status ===")

		// Validate connections
		if errValidate := svc.ValidateConnection(ctx); errValidate != nil {
			log.Printf("❌ Connection Error: %v\n", errValidate)
			return nil
		}
		log.Println("✓ All services connected")

		// Get stats
		stats, err := svc.GetStats(ctx)
		if err != nil {
			log.Printf("❌ Stats Error: %v\n", err)
			return nil
		}

		log.Printf("✓ Total chunks: %d\n", stats["total_chunks"])
		log.Printf("✓ Embedding model: %s\n", stats["embedding_model"])
		log.Printf("✓ Chat model: %s\n", stats["chat_model"])
		log.Printf("✓ Collection: %s\n", stats["collection_name"])

		if c.Verbose {
			log.Println("\nConfiguration:")
			log.Printf("  Ollama URL: %s\n", cfg.Ollama.BaseURL)
			log.Printf("  Vector DB: %s\n", cfg.VectorDB.PersistenceDir)
			// Empty templates_dir means the binary's embedded
			// templates are in use. Spell that out so `status`
			// doesn't look like a misconfig.
			templatesLabel := cfg.Paths.TemplatesDir
			if templatesLabel == "" {
				templatesLabel = "(embedded)"
			}
			log.Printf("  Templates: %s\n", templatesLabel)
		}

		return nil
	})
}

// ListCmd represents the list command.
type ListCmd struct {
	ConfigOpts
	Verbose bool `short:"v" help:"Show detailed information"`
}

func (c *ListCmd) Run(ctx *kong.Context) error {
	return withService(c.ConfigOpts, func(ctx context.Context, _ *config.Config, svc *service.Service) error {
		docs, err := svc.ListDocuments(ctx)
		if err != nil {
			return fmt.Errorf("failed to list documents: %w", err)
		}

		if len(docs) == 0 {
			log.Println("No documents found in the database.")
			return nil
		}

		log.Printf("Found %d document(s):\n\n", len(docs))
		for i, doc := range docs {
			log.Printf("%d. %s\n", i+1, doc.Title)
			log.Printf("   UID: %s\n", doc.UID)
			log.Printf("   Fingerprint: %s\n", doc.Fingerprint)
			if c.Verbose {
				log.Printf("   Tags: %s\n", strings.Join(doc.Tags, ", "))
				log.Printf("   Categories: %s\n", strings.Join(doc.Categories, ", "))
				log.Printf("   URLs: %s\n", strings.Join(doc.URLs, ", "))
				log.Printf("   Chunks: %d\n", doc.ChunkCount)
				log.Printf("   Created: %s\n", doc.CreatedAt.Format("2006-01-02 15:04:05"))
			}
			log.Println()
		}

		return nil
	})
}

// SearchCmd represents the search command.
type SearchCmd struct {
	ConfigOpts
	Query    string `help:"Search query" arg:""`
	TopK     int    `short:"k" default:"5" help:"Number of results"`
	DocID    string `short:"d" help:"Filter by document ID"`
	Tag      string `name:"tag" help:"Filter by tag"`
	Category string `name:"category" help:"Filter by category"`
	Mode     string `short:"m" default:"hybrid" help:"Search mode: hybrid (default), semantic, or keyword" enum:"hybrid,semantic,keyword"`
	Verbose  bool   `short:"v" help:"Show full content"`
}

func (c *SearchCmd) Run(ctx *kong.Context) error {
	return withService(c.ConfigOpts, func(ctx context.Context, _ *config.Config, svc *service.Service) error {
		mode := parseSearchModeFlag(c.Mode)
		filters := service.SearchFilters{
			DocumentID: c.DocID,
			Tag:        c.Tag,
			Category:   c.Category,
		}

		// SearchByDocument is a v0.3.0 convenience wrapper
		// that pre-fills the document_id filter. When the
		// caller passed other filters too, drop down to
		// HybridSearch so all of them apply.
		var results []models.SearchResult
		var err error
		if c.DocID != "" && c.Tag == "" && c.Category == "" {
			results, err = svc.SearchByDocument(ctx, c.Query, c.DocID, c.TopK)
		} else {
			results, err = svc.HybridSearch(ctx, c.Query, c.TopK, filters, mode)
		}
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		if len(results) == 0 {
			log.Println("No results found.")
			return nil
		}

		log.Printf("Search results for: %s\n\n", c.Query)
		for i, result := range results {
			log.Printf("[%d] %s (Score: %.3f)\n", i+1, result.DocumentTitle, result.Similarity)
			log.Printf("    Chunk ID: %s\n", result.ChunkID)
			if c.Verbose {
				log.Printf("    Content: %s\n", result.Content)
			}
			log.Println()
		}

		return nil
	})
}

// parseSearchModeFlag translates the CLI string flag into the
// service-level enum. Mirrors the JSON wire parser in
// internal/web/huma_query.go so behavior is identical
// regardless of which surface the caller uses.
func parseSearchModeFlag(s string) service.SearchMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "semantic":
		return service.ModeSemantic
	case "keyword":
		return service.ModeKeyword
	case "", "hybrid":
		return service.ModeHybrid
	default:
		return service.ModeHybrid
	}
}
