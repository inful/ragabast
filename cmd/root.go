package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/ragabast/internal/web"
)

// CLI represents the main command-line interface structure.
type CLI struct {
	Ingest IngestCmd     `cmd:"" help:"Ingest and process docubilder documents"`
	Query  QueryCmd      `cmd:"" help:"Query the vector database with natural language"`
	Serve  ServeCmd      `cmd:"" help:"Start the web server"`
	Init   ConfigInitCmd `cmd:"" help:"Write a starter config file (alias for 'config init')"`
	Config ConfigCmd     `cmd:"" help:"Manage configuration"`
	Status StatusCmd     `cmd:"" help:"Check system health and status"`
	List   ListCmd       `cmd:"" help:"List ingested documents"`
	Search SearchCmd     `cmd:"" help:"Semantic search across documents"`
}

type ConfigOpts struct {
	Config string `short:"c" name:"config" env:"RAGABAST_CONFIG" help:"Path to config file (YAML). If not set, tries config.yml then config.yaml."`
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
	Files []string `help:"Docubilder files to ingest" arg:"" type:"existingfile"`
	Path  string   `short:"p" help:"Directory path to scan for documents"`
}

func (c *IngestCmd) Run(ctx *kong.Context) error {
	cfg, err := config.Load(c.Config)
	if err != nil {
		return err
	}

	svc, err := service.NewService(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize service: %w", err)
	}

	ctxApp := context.Background()

	// Ingest files
	if len(c.Files) > 0 {
		for _, file := range c.Files {
			log.Printf("Ingesting file: %s\n", file)
			if err := svc.IngestFile(ctxApp, file); err != nil {
				log.Printf("Error ingesting %s: %v\n", file, err)
			} else {
				log.Printf("✓ Successfully ingested: %s\n", file)
			}
		}
	}

	// Ingest directory
	if c.Path != "" {
		log.Printf("Scanning directory: %s\n", c.Path)
		if err := svc.IngestDirectory(ctxApp, c.Path); err != nil {
			return fmt.Errorf("failed to ingest directory: %w", err)
		}
	}

	if len(c.Files) == 0 && c.Path == "" {
		return errors.New("no files or directory specified. Use --help for usage")
	}

	return nil
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
	cfg, err := config.Load(c.Config)
	if err != nil {
		return err
	}

	svc, err := service.NewService(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize service: %w", err)
	}

	ctxApp := context.Background()

	log.Printf("Querying: %s\n", c.Query)
	var response string
	var debug *service.QueryDebugInfo
	if c.Verbose {
		response, debug, err = svc.QueryDebugWithOptions(ctxApp, c.Query, c.TopK, service.LLMOptions{Temperature: c.Temperature})
	} else {
		if c.Temperature != nil {
			response, _, err = svc.QueryDebugWithOptions(ctxApp, c.Query, c.TopK, service.LLMOptions{Temperature: c.Temperature})
		} else {
			response, err = svc.Query(ctxApp, c.Query, c.TopK)
		}
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
}

// ServeCmd represents the serve command.
type ServeCmd struct {
	ConfigOpts
	Host string `short:"h" help:"Server host (overrides config)"`
	Port int    `short:"p" help:"Server port (overrides config)"`
}

func (c *ServeCmd) Run(ctx *kong.Context) error {
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
	cfg, err := config.Load(c.Config)
	if err != nil {
		return err
	}

	svc, err := service.NewService(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize service: %w", err)
	}

	ctxApp := context.Background()

	log.Println("=== RAG System Status ===")

	// Validate connections
	if errValidate := svc.ValidateConnection(ctxApp); errValidate != nil {
		log.Printf("❌ Connection Error: %v\n", errValidate)
		return nil
	}
	log.Println("✓ All services connected")

	// Get stats
	stats, err := svc.GetStats(ctxApp)
	if err != nil {
		log.Printf("❌ Stats Error: %v\n", err)
		return nil
	}

	log.Printf("✓ Total chunks: %d\n", stats["total_chunks"])
	log.Printf("✓ Embedding model: %s\n", stats["embedding_model"])
	log.Printf("✓ Generation model: %s\n", stats["generation_model"])
	log.Printf("✓ Collection: %s\n", stats["collection_name"])

	if c.Verbose {
		log.Println("\nConfiguration:")
		log.Printf("  Ollama URL: %s\n", cfg.Ollama.BaseURL)
		log.Printf("  Vector DB: %s\n", cfg.VectorDB.PersistenceDir)
		log.Printf("  Templates: %s\n", cfg.Paths.TemplatesDir)
	}

	return nil
}

// ListCmd represents the list command.
type ListCmd struct {
	ConfigOpts
	Verbose bool `short:"v" help:"Show detailed information"`
}

func (c *ListCmd) Run(ctx *kong.Context) error {
	cfg, err := config.Load(c.Config)
	if err != nil {
		return err
	}

	svc, err := service.NewService(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize service: %w", err)
	}

	ctxApp := context.Background()

	docs, err := svc.ListDocuments(ctxApp)
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
}

// SearchCmd represents the search command.
type SearchCmd struct {
	ConfigOpts
	Query   string `help:"Search query" arg:""`
	TopK    int    `short:"k" default:"5" help:"Number of results"`
	DocID   string `short:"d" help:"Filter by document ID"`
	Verbose bool   `short:"v" help:"Show full content"`
}

func (c *SearchCmd) Run(ctx *kong.Context) error {
	cfg, err := config.Load(c.Config)
	if err != nil {
		return err
	}

	svc, err := service.NewService(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize service: %w", err)
	}

	ctxApp := context.Background()

	// Perform search
	var results []models.SearchResult
	if c.DocID != "" {
		results, err = svc.SearchByDocument(ctxApp, c.Query, c.DocID, c.TopK)
	} else {
		results, err = svc.Search(ctxApp, c.Query, c.TopK, nil)
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
		log.Printf("[%d] %s (Similarity: %.3f)\n", i+1, result.DocumentTitle, result.Similarity)
		log.Printf("    Chunk ID: %s\n", result.ChunkID)
		if c.Verbose {
			log.Printf("    Content: %s\n", result.Content)
		}
		log.Println()
	}

	return nil
}
