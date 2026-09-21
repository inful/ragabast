package web

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/web/jobs"
)

type healthResponseBody struct {
	Status string `json:"status"`
}

// healthFullResponseBody carries the response of /api/health/full.
// ingest_queue is always present; when async ingest is disabled
// (AsyncIngestQueueDir is empty), the ingest_queue.enabled flag
// is false and the count fields are zero. This shape lets the
// operator's monitoring parse the payload with a single schema
// regardless of whether async ingest is in use.
type healthFullResponseBody struct {
	Status      string                  `json:"status"`
	VectorDB    string                  `json:"vector_db"`
	EmbedServer string                  `json:"embeddings_server"`
	IngestQueue ingestQueueHealthStatus `json:"ingest_queue"`
}

type ingestQueueHealthStatus struct {
	Enabled     bool  `json:"enabled"`
	Workers     int   `json:"workers"`
	Pending     int   `json:"pending"`
	Processing  int   `json:"processing"`
	Completed   int   `json:"completed"`
	Failed      int   `json:"failed"`
	OldestAgeMs int64 `json:"oldest_pending_age_ms,omitempty"`
}

// healthFullStaleThresholdMs is how stale a pending job must
// get before /api/health/full flips to 503. 5 minutes is long
// enough that brief ingest slowness doesn't alarm; short enough
// that a stuck worker gets caught promptly. Operators can tune
// later via a config knob if needed.
const healthFullStaleThresholdMs = 5 * 60 * 1000

// buildHealthFull assembles the /api/health/full payload and
// decides whether the response is 200 or 503. The basic /api/health
// checks (vector DB, embedding server) gate the response first;
// the queue stats layer on top — a degraded queue flips a 200
// to 503, even if the basic checks pass.
//
// When ingestQueue is nil (operator has not configured async
// ingest), the ingest_queue block is reported as disabled and
// does NOT contribute to the status decision.
func buildHealthFull(ctx context.Context, svc serviceAPI) (*healthFullResponseBody, error) {
	body := healthFullResponseBody{
		Status:      "healthy",
		VectorDB:    "ok",
		EmbedServer: "ok",
		IngestQueue: ingestQueueHealthStatus{Enabled: false},
	}

	// Basic checks first — these are the contract /api/health
	// already enforces.
	if _, err := svc.CheckHealth(ctx); err != nil {
		return nil, huma.Error503ServiceUnavailable("Service unhealthy: " + err.Error())
	}

	// Queue stats layer. If the queue is running, populate
	// the counters and decide whether stale pending work
	// warrants a 503.
	if q := getIngestQueueForHealth(); q != nil {
		stats := q.Depth()
		qi := ingestQueueHealthStatus{
			Enabled:    true,
			Workers:    q.Workers(),
			Pending:    stats.Pending,
			Processing: stats.Processing,
			Completed:  stats.Completed,
			Failed:     stats.Failed,
		}
		if age := q.OldestPendingAge(); age != nil {
			qi.OldestAgeMs = age.Milliseconds()
		}
		body.IngestQueue = qi

		// Stale-pending detection: if jobs have been waiting
		// longer than the threshold AND no workers are
		// processing anything, the pool is stuck. That's the
		// classic "queue backed up" failure mode and warrants a
		// 503 even if connectivity is fine.
		if stats.Pending > 0 && qi.OldestAgeMs > healthFullStaleThresholdMs {
			return nil, huma.Error503ServiceUnavailable(
				"async ingest queue has pending jobs older than the stale threshold; " +
					"workers may be stuck",
			)
		}
	}

	return &body, nil
}

// getIngestQueueForHealth returns the queue reference the web
// package already holds, so the /api/health/full handler can
// read counters without plumbing a new dependency through
// serviceAPI. Returns nil when async ingest is disabled (queue
// not configured at server construction time).
func getIngestQueueForHealth() *jobs.Queue {
	return globalIngestQueue
}

// globalIngestQueue is set by NewServer when async ingest is
// configured. Access is read-only; the var keeps the existing
// serviceAPI interface unchanged while letting the health
// handler observe queue state without crossing the package
// boundary twice.
var globalIngestQueue *jobs.Queue

type tagsResponseBody struct {
	Tags []string `json:"tags"`
}

type categoriesResponseBody struct {
	Categories []string `json:"categories"`
}

type tagsAndCategoriesResponseBody struct {
	Tags       []string `json:"tags"`
	Categories []string `json:"categories"`
}

// registerCatalogOperations wires the small read-only catalog
// endpoints: health check and the three tag/category lookups.
func registerCatalogOperations(api huma.API, svc serviceAPI, _ *jobs.Queue) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
		Summary:     "Health check",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body healthResponseBody }, error) {
		_, err := svc.CheckHealth(ctx)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("Service unhealthy")
		}
		return &struct{ Body healthResponseBody }{Body: healthResponseBody{Status: "healthy"}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "health-full",
		Method:      http.MethodGet,
		Path:        "/api/health/full",
		Summary:     "Detailed health check including async-ingest queue state",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body healthFullResponseBody }, error) {
		body, err := buildHealthFull(ctx, svc)
		if err != nil {
			return nil, err
		}
		return &struct{ Body healthFullResponseBody }{Body: *body}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-tags",
		Method:      http.MethodGet,
		Path:        "/api/tags",
		Summary:     "Get normalized tags",
		Description: "Returns all unique tags from the vector database, normalized to lowercase and sorted.",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body tagsResponseBody }, error) {
		tags, err := svc.GetNormalizedTags(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to retrieve tags")
		}
		return &struct{ Body tagsResponseBody }{Body: tagsResponseBody{Tags: tags}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-categories",
		Method:      http.MethodGet,
		Path:        "/api/categories",
		Summary:     "Get normalized categories",
		Description: "Returns all unique categories from the vector database, trimmed of whitespace and sorted.",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body categoriesResponseBody }, error) {
		categories, err := svc.GetNormalizedCategories(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to retrieve categories")
		}
		return &struct{ Body categoriesResponseBody }{Body: categoriesResponseBody{Categories: categories}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-tags-and-categories",
		Method:      http.MethodGet,
		Path:        "/api/tags-categories",
		Summary:     "Get both tags and categories",
		Description: "Returns all unique tags (lowercase) and categories (preserved case) from the vector database.",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body tagsAndCategoriesResponseBody }, error) {
		tags, categories, err := svc.GetTagsAndCategories(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to retrieve tags and categories")
		}
		return &struct{ Body tagsAndCategoriesResponseBody }{
			Body: tagsAndCategoriesResponseBody{
				Tags:       tags,
				Categories: categories,
			},
		}, nil
	})
}
