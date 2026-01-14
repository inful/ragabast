package web

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/ragabast/internal/config"
)

func registerHumaAPI(router chi.Router, appCfg *config.Config, svc serviceAPI) huma.API {
	humaCfg := huma.DefaultConfig("Ragabast API", "1.0.0")
	humaCfg.DocsPath = "/docs"
	humaCfg.OpenAPIPath = "/openapi.json"

	api := humachi.New(router, humaCfg)

	// Limit concurrent ingestion requests to avoid overloading downstream dependencies.
	maxConcurrent := 5
	if appCfg != nil && appCfg.Processing.MaxConcurrentProcessing > 0 {
		maxConcurrent = appCfg.Processing.MaxConcurrentProcessing
	}
	limiter := NewIngestLimiter(maxConcurrent, 1*time.Second)

	RegisterHumaOperations(api, svc, limiter)
	return api
}
