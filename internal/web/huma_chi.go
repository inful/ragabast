package web

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/ragabast/internal/config"
)

func registerHumaAPI(router chi.Router, _ *config.Config, svc serviceAPI) huma.API {
	humaCfg := huma.DefaultConfig("Ragabast API", "1.0.0")
	humaCfg.DocsPath = "/docs"
	humaCfg.OpenAPIPath = "/openapi.json"

	api := humachi.New(router, humaCfg)

	// Limit concurrent ingestion requests to avoid overloading
	// downstream dependencies. The default of 5 matches the historical
	// config-driven behavior now that MaxConcurrentProcessing has
	// been retired.
	limiter := NewIngestLimiter(5, 1*time.Second)

	registerHumaOperations(api, svc, limiter)
	return api
}
