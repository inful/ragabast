// Package vector includes a text search index backed by bleve.
//
// The text index complements the embedding-based semantic
// search in this package. Bleve provides BM25 scoring,
// persistent storage, and a query parser (phrase, boolean,
// field filters) so we don't have to maintain those.
//
// Index shape: one bleve document per chunk, keyed by chunk
// ID. Each document has fields:
//
//   - id              string  (primary key, the chunk ID)
//   - content         text    (analyzer: technical)
//   - document_id     keyword (filter)
//   - tags            text[]  (multi-value; filter via term query)
//   - categories      text[]  (multi-value; filter via term query)
//   - title           text    (for snippet display)
//
// The analyzer name "technical" is registered in buildMapping
// and configured to match the rules Tokenize() used before the
// pivot: split on non-alphanumerics but preserve runs of
// [A-Za-z0-9_-]+ as a single token, drop short tokens, lowercase.
package vector

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goregexp "regexp"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/token/length"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/regexp"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/registry"
	"github.com/blevesearch/bleve/v2/search/query"
)

// SearchFilters narrows a Search by document-level attributes.
// Empty fields are ignored. When DocumentID is set, only chunks
// belonging to that document match. Tag and Category filter via
// term queries against the multi-value fields; a chunk matches
// when its tag set contains the filter value (exact match, not
// substring — bleve's term query gives us that for free).
type SearchFilters struct {
	DocumentID string
	Tag        string
	Category   string
}

// SearchHit is one ranked result from a SearchIndex.Search.
type SearchHit struct {
	ID         string
	DocumentID string
	Score      float32
}

// SearchIndexPathFor returns the on-disk path for the keyword
// index, given the vector DB's persistence dir. Both stores
// share a parent dir so a single `vector reset` cleans both up.
// Returns "" when vectorPersistenceDir is empty so the caller
// can detect a misconfigured pipeline.
func SearchIndexPathFor(vectorPersistenceDir string) string {
	if vectorPersistenceDir == "" {
		return ""
	}
	return filepath.Join(vectorPersistenceDir, "search")
}

// SearchIndex wraps a bleve.Index with chunk-level operations:
// batch ingest, delete by chunk id, delete by document id, and
// filtered keyword search. All operations are safe for
// concurrent use; bleve serializes writes internally.
type SearchIndex struct {
	index bleve.Index
	path  string
}

// analyzerName is the name we register the custom analyzer
// under. Matches the doc comments above.
const analyzerName = "technical"

// regexTokenizerName / regexLengthFilterName are the names
// under which we register the custom tokenizer + length filter
// in init() so the analyzer config (registered later) can
// reference them by string.
const (
	regexTokenizerName = "ragabast_technical_tokenizer"
	regexLengthName    = "ragabast_technical_length"
)

// init registers the custom tokenizer and length filter with
// bleve's global registry at process start.
//
// bleve resolves analyzer names by looking them up in its
// registry when parsing a persisted mapping. Lazy registration
// (gated on sync.Once inside BuildMapping) only fires when the
// caller goes through NewSearchIndex; LoadSearchIndex calls
// bleve.Open directly, which means the registry is empty on
// restart and bleve refuses to load the index with
// "no tokenizer with name or type 'ragabast_technical_tokenizer'
// registered". Registration must run before any code touches
// the registry, so init() is the only correct location.
//
//nolint:gochecknoinits // bleve's global registry must be populated before any index open.
func init() {
	// Tokenizer: split on the regex `[\p{L}\p{N}_-]+`. This
	// matches runs of letter, digit, underscore, or hyphen
	// — the same set Tokenize() used to emit. Every other
	// rune is a token boundary.
	tokenizerRE := mustCompileRegex(`[\p{L}\p{N}_-]+`)
	if err := registry.RegisterTokenizer(regexTokenizerName, func(_ map[string]any, _ *registry.Cache) (analysis.Tokenizer, error) {
		return regexp.NewRegexpTokenizer(tokenizerRE), nil
	}); err != nil {
		// Bleve returns ErrAlreadyDefined when a second init
		// runs (e.g. test binary re-exec); treat as success.
		if !errors.Is(err, registry.ErrAlreadyDefined) {
			panic(fmt.Sprintf("ragabast: register regex tokenizer: %v", err))
		}
	}

	// Length filter: drop tokens shorter than 2 chars. We
	// register our own (with the exact min we want) rather
	// than reusing bleve's stock length filter, because the
	// bleve stock filter requires explicit min/max in the
	// mapping config and we want a single, named, configured
	// instance.
	if err := registry.RegisterTokenFilter(regexLengthName, func(_ map[string]any, _ *registry.Cache) (analysis.TokenFilter, error) {
		return length.NewLengthFilter(2, 1024), nil
	}); err != nil {
		if !errors.Is(err, registry.ErrAlreadyDefined) {
			panic(fmt.Sprintf("ragabast: register length filter: %v", err))
		}
	}
}

// mustCompileRegex panics on bad regex so the init-time
// constant is unambiguous.
func mustCompileRegex(pat string) *goregexp.Regexp {
	re, err := goregexp.Compile(pat)
	if err != nil {
		panic(fmt.Sprintf("ragabast: invalid regex %q: %v", pat, err))
	}
	return re
}

// BuildMapping returns the index mapping for the search
// index. Exposed at package level so doctor/reset commands
// can inspect field definitions.
func BuildMapping() (*mapping.IndexMappingImpl, error) {
	m := bleve.NewIndexMapping()

	// Custom analyzer: our regex tokenizer + lowercase +
	// min-length-2 filter. Matches the prior Tokenize() rules.
	if err := m.AddCustomAnalyzer(analyzerName, map[string]any{
		"type":          custom.Name,
		"tokenizer":     regexTokenizerName,
		"token_filters": []string{lowercase.Name, regexLengthName},
	}); err != nil {
		return nil, fmt.Errorf("register analyzer: %w", err)
	}

	contentF := bleve.NewTextFieldMapping()
	contentF.Analyzer = analyzerName

	titleF := bleve.NewTextFieldMapping()
	titleF.Analyzer = analyzerName

	docIDF := bleve.NewKeywordFieldMapping()
	tagsF := bleve.NewTextFieldMapping()
	tagsF.Analyzer = analyzerName
	catsF := bleve.NewTextFieldMapping()
	catsF.Analyzer = analyzerName

	docMapping := bleve.NewDocumentMapping()
	docMapping.AddFieldMappingsAt("content", contentF)
	docMapping.AddFieldMappingsAt("title", titleF)
	docMapping.AddFieldMappingsAt("document_id", docIDF)
	docMapping.AddFieldMappingsAt("tags", tagsF)
	docMapping.AddFieldMappingsAt("categories", catsF)

	m.AddDocumentMapping("_default", docMapping)
	return m, nil
}

// chunkDoc is the bleve document we index per chunk. Field
// names are short (lower-case snake) to keep the on-disk
// representation tight — the field count grows linearly with
// the corpus.
type chunkDoc struct {
	Content    string   `json:"content"`
	Title      string   `json:"title"`
	DocumentID string   `json:"document_id"`
	Tags       []string `json:"tags"`
	Categories []string `json:"categories"`
}

// chunkSummary is the minimum subset of *models.Chunk that
// SearchIndex needs. Defined locally so the vector package
// doesn't have to reach into models just to know which fields
// to index. The caller passes the populated struct from the
// service layer (which already has a models.Chunk).
type chunkSummary struct {
	ID         string
	Content    string
	Title      string
	DocumentID string
	Tags       []string
	Categories []string
}

// NewSearchIndex opens or creates a bleve index at path. The
// parent directory is created if missing; bleve stores its
// files directly under path.
func NewSearchIndex(path string) (*SearchIndex, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create search index dir: %w", err)
	}
	m, err := BuildMapping()
	if err != nil {
		return nil, err
	}
	idx, err := bleve.New(path, m)
	if err != nil {
		return nil, fmt.Errorf("open search index: %w", err)
	}
	return &SearchIndex{index: idx, path: path}, nil
}

// LoadSearchIndex opens an existing bleve index at path. If
// no index exists at path (e.g. on first run after the v0.4
// upgrade), it creates one.
func LoadSearchIndex(path string) (*SearchIndex, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewSearchIndex(path)
		}
		return nil, fmt.Errorf("stat search index: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("search index path %s is not a directory", path)
	}
	idx, err := bleve.Open(path)
	if err != nil {
		if errors.Is(err, bleve.ErrorIndexPathDoesNotExist) {
			return NewSearchIndex(path)
		}
		return nil, fmt.Errorf("open search index: %w", err)
	}
	return &SearchIndex{index: idx, path: path}, nil
}

// Close releases the index's file handles. bleve stores its
// state under path and reopens on next load.
func (s *SearchIndex) Close() error {
	if s == nil || s.index == nil {
		return nil
	}
	return s.index.Close()
}

// Path returns the on-disk location of the index. Used by the
// doctor command to verify the path is reachable.
func (s *SearchIndex) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Size returns the number of indexed chunks.
func (s *SearchIndex) Size() (int, error) {
	if s == nil || s.index == nil {
		return 0, nil
	}
	n, err := s.index.DocCount()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// AddBatch indexes every chunk in one atomic batch. bleve
// applies the batch in a single transaction; either every
// chunk is visible to subsequent searches or none of them is.
func (s *SearchIndex) AddBatch(chunks []chunkSummary) error {
	if s == nil || s.index == nil {
		return errors.New("search index not initialized")
	}
	if len(chunks) == 0 {
		return nil
	}
	batch := s.index.NewBatch()
	for _, c := range chunks {
		doc := chunkDoc{
			Content:    c.Content,
			Title:      c.Title,
			DocumentID: c.DocumentID,
			Tags:       c.Tags,
			Categories: c.Categories,
		}
		if err := batch.Index(c.ID, doc); err != nil {
			return fmt.Errorf("index chunk %s: %w", c.ID, err)
		}
	}
	if err := s.index.Batch(batch); err != nil {
		return fmt.Errorf("commit batch: %w", err)
	}
	return nil
}

// DeleteChunk removes id from the index. Missing IDs are
// silently ignored — bleve's Delete is idempotent.
func (s *SearchIndex) DeleteChunk(id string) error {
	if s == nil || s.index == nil {
		return errors.New("search index not initialized")
	}
	if id == "" {
		return nil
	}
	return s.index.Delete(id)
}

// DeleteDocument removes every chunk that belongs to documentID.
// bleve doesn't have a direct "delete by field" so we issue a
// term query and delete each match. For a few hundred thousand
// chunks this is sub-second; if it ever becomes a bottleneck,
// we can switch to a managed-search helper.
func (s *SearchIndex) DeleteDocument(documentID string) error {
	if s == nil || s.index == nil {
		return errors.New("search index not initialized")
	}
	if documentID == "" {
		return nil
	}
	q := bleve.NewTermQuery(documentID)
	q.SetField("document_id")
	req := bleve.NewSearchRequest(q)
	req.Size = 1_000_000 // bleve's max page size
	res, err := s.index.Search(req)
	if err != nil {
		return fmt.Errorf("scan document_id=%s: %w", documentID, err)
	}
	for _, hit := range res.Hits {
		if err := s.index.Delete(hit.ID); err != nil {
			return fmt.Errorf("delete chunk %s: %w", hit.ID, err)
		}
	}
	return nil
}

// Clear empties the index. Used by the vector-reset path
// (mirrors chromem-go's Clear).
func (s *SearchIndex) Clear() error {
	if s == nil || s.index == nil {
		return errors.New("search index not initialized")
	}
	// bleve exposes Close on the index; recreating the
	// directory is the simplest way to drop every document.
	_ = s.index.Close()
	if err := os.RemoveAll(s.path); err != nil {
		return fmt.Errorf("clear search index: %w", err)
	}
	idx, err := NewSearchIndex(s.path)
	if err != nil {
		return err
	}
	s.index = idx.index
	return nil
}

// Search returns up to limit hits for query, sorted descending
// by score. Filters are pre-applied so the top of the ranking
// reflects filter compliance, not just relevance.
//
// query is fed into a MatchQuery against the content field.
// MatchQuery is analyzer-aware, so it preserves hyphenated
// identifiers (e.g. "tls-handshake-failure") that the
// query-string parser would split on `-`. Multi-word queries
// match any token (OR semantics); for stricter matching, wrap
// the query in the phrase syntax (`"exact phrase"`) in a
// future revision, or combine filters with the search.
//
// Bleve's query-string parser is NOT used because its lexer
// splits on `-` regardless of the analyzer configuration,
// which breaks identifier recall — the whole point of the
// hybrid BM25+semantic path.
func (s *SearchIndex) Search(query string, limit int, filters SearchFilters) ([]SearchHit, error) {
	if s == nil || s.index == nil {
		return nil, errors.New("search index not initialized")
	}
	if limit <= 0 {
		limit = 5
	}

	q := buildSearchQuery(query, filters)
	req := bleve.NewSearchRequest(q)
	req.Size = limit
	req.Fields = []string{"document_id"}

	res, err := s.index.Search(req)
	if err != nil {
		return nil, fmt.Errorf("search index query: %w", err)
	}
	hits := make([]SearchHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		docID := ""
		if v, ok := h.Fields["document_id"].(string); ok {
			docID = v
		}
		hits = append(hits, SearchHit{
			ID:         h.ID,
			DocumentID: docID,
			Score:      float32(h.Score),
		})
	}
	return hits, nil
}

// buildSearchQuery combines the user query with any filters.
// The user query becomes a MatchQuery against the content
// field; filters add term/match constraints on metadata fields.
// All clauses are AND'd together (ConjunctionQuery).
func buildSearchQuery(userQuery string, f SearchFilters) query.Query {
	var main query.Query
	if userQuery == "" {
		main = bleve.NewMatchAllQuery()
	} else {
		mq := bleve.NewMatchQuery(userQuery)
		mq.SetField("content")
		main = mq
	}
	clauses := []query.Query{main}
	if d := trimSpace(f.DocumentID); d != "" {
		t := bleve.NewTermQuery(d)
		t.SetField("document_id")
		clauses = append(clauses, t)
	}
	if t := trimSpace(f.Tag); t != "" {
		mq := bleve.NewMatchQuery(t)
		mq.SetField("tags")
		clauses = append(clauses, mq)
	}
	if c := trimSpace(f.Category); c != "" {
		mq := bleve.NewMatchQuery(c)
		mq.SetField("categories")
		clauses = append(clauses, mq)
	}
	if len(clauses) == 1 {
		return main
	}
	return bleve.NewConjunctionQuery(clauses...)
}

func trimSpace(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
