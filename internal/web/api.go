package web

import (
	"context"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/ragabast/internal/vector"
)

// serviceAPI is the slice of *service.Service that the web layer
// depends on. Lives in its own file so the contract between web
// and service is visible without scrolling through server.go's
// lifecycle code. Tests substitute a fake; production gets the
// real *service.Service via web.NewServer.
type serviceAPI interface {
	CheckHealth(ctx context.Context) (bool, error)
	IngestDocument(ctx context.Context, content string) (*models.Document, error)
	Search(ctx context.Context, query string, limit int, filters service.SearchFilters) ([]models.SearchResult, error)
	HybridSearch(ctx context.Context, query string, limit int, filters service.SearchFilters, mode service.SearchMode) ([]models.SearchResult, error)
	ListDocuments(ctx context.Context) ([]models.DocumentInfo, error)
	ListDocumentsPaged(ctx context.Context, limit, offset int) (docs []models.DocumentInfo, total int, err error)
	DeleteDocument(ctx context.Context, documentID string) error
	BulkUpdateDocuments(ctx context.Context, patches []vector.DocumentMetadataPatch, mode string) ([]service.BulkUpdateDocumentsResult, error)
	SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (service.FrontmatterSuggestion, error)
	QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error)
	QueryCacheStats() service.QueryCacheStats
	ChatSessionHistory(sessionID string) []service.ChatMessage
	ChatSessionSourceKinds(sessionID string) []string
	SetChatSessionSourceKinds(sessionID string, kinds []string)
	AppendChatTurn(ctx context.Context, sessionID string, exchange ...service.ChatMessage) error
	ClearChatSession(sessionID string)
	GetDocument(ctx context.Context, documentID string) (*models.Document, error)
	RelatedDocuments(ctx context.Context, uid string, limit int) ([]vector.RelatedDocument, error)

	// WebMirrors returns the mirrored web pages and their state —
	// the effective list of what was requested, allowed, and
	// fetched.
	WebMirrors() []service.WebMirror

	// AllowedWebHosts returns the hosts ragabast may fetch from.
	AllowedWebHosts() []string

	// StartWebMirrorRefresher starts the background refresher that
	// re-fetches the mirrored web pages whose interval elapsed.
	StartWebMirrorRefresher(ctx context.Context)
	GetDocumentFingerprint(ctx context.Context, uid string) (service.DocumentFingerprintInfo, bool, error)
	GetNormalizedTags(ctx context.Context) ([]string, error)
	GetNormalizedCategories(ctx context.Context) ([]string, error)
	GetTagsAndCategories(ctx context.Context) (tags []string, categories []string, err error)

	// StopChatSessionsGC stops the chat-session idle-TTL
	// GC goroutine. The web Server's Stop calls this so a
	// clean shutdown doesn't leave a hung goroutine. Tests
	// can also call it; nil-safe on the receiver.
	StopChatSessionsGC()

	// Close releases the resources the service holds (the bleve
	// keyword index behind the vector layer). The server calls it
	// after the HTTP listener is down, so in-flight requests can
	// still read the index.
	Close() error
}

// Compile-time assertion that *service.Service satisfies serviceAPI.
// Without this, adding a method to serviceAPI that *service.Service
// does not implement would compile fine and only blow up at runtime
// when web.NewServer is handed the wrong type. With this, drift is
// caught at `go build` time. The same check applied via reflection
// (e.g. in a test) would miss the next divergence; the compiler
// check survives indefinitely.
var _ serviceAPI = (*service.Service)(nil)
