package web

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

func registerHumaAPI(router chi.Router, svc serviceAPI) huma.API {
	cfg := huma.DefaultConfig("Ragabast API", "1.0.0")
	cfg.DocsPath = "/docs"
	cfg.OpenAPIPath = "/openapi.json"

	api := humachi.New(router, cfg)
	RegisterHumaOperations(api, svc)
	return api
}
