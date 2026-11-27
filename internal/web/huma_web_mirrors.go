package web

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// webMirrorEntry is one mirrored page in the effective list.
type webMirrorEntry struct {
	SourceURL  string   `doc:"The page that was fetched." json:"source_url"`
	UID        string   `doc:"UID of the mirror document in the corpus." json:"uid"`
	Title      string   `doc:"Title of the mirrored page, when it has one." json:"title,omitempty"`
	DeclaredBy []string `doc:"UIDs of the documents that requested this page." json:"declared_by,omitempty"`
	FetchedAt  string   `doc:"When the page was last fetched (RFC 3339)." json:"fetched_at,omitempty"`
	Error      string   `doc:"The last failure, empty when the last fetch worked." json:"error,omitempty"`
}

// webMirrorsResponseBody is the JSON shape returned by
// GET /api/web-mirrors.
type webMirrorsResponseBody struct {
	// AllowedHosts are the hosts ragabast is permitted to fetch
	// from. Empty means the feature is off.
	AllowedHosts []string `doc:"Hosts ragabast may fetch from (the operator's allowlist)." json:"allowed_hosts"`
	// Mirrors are the pages currently mirrored into the corpus.
	Mirrors []webMirrorEntry `doc:"The mirrored pages and their state." json:"mirrors"`
}

// registerWebMirrorOperations wires GET /api/web-mirrors.
//
// The endpoint is the operator's view of the effective list: the
// pages that were requested by a document, allowed by the policy,
// and fetched — with the documents that requested each one and the
// outcome of the last fetch.
func registerWebMirrorOperations(api huma.API, svc serviceAPI) {
	registerWithInput[struct{}, webMirrorsResponseBody](api,
		huma.Operation{
			OperationID: "web-mirrors",
			Method:      http.MethodGet,
			Path:        "/api/web-mirrors",
			Summary:     "List the external web pages mirrored into the corpus",
			Description: "Returns the effective list of mirrored pages: the external URLs documents requested through their `web_sources:` frontmatter, filtered by the operator's `web_ingest.allowed_hosts` policy. Each entry names the source URL, the mirror document's UID, the documents that requested it, and the outcome of the last fetch.",
		},
		func(ctx context.Context, _ *struct{}) (*webMirrorsResponseBody, error) {
			mirrors := svc.WebMirrors()
			out := make([]webMirrorEntry, 0, len(mirrors))
			for _, mirror := range mirrors {
				out = append(out, webMirrorEntry{
					SourceURL:  mirror.SourceURL,
					UID:        mirror.UID,
					Title:      mirror.Title,
					DeclaredBy: mirror.DeclaredBy,
					FetchedAt:  mirror.FetchedAt.Format("2006-01-02T15:04:05Z07:00"),
					Error:      mirror.Error,
				})
			}

			return &webMirrorsResponseBody{
				AllowedHosts: svc.AllowedWebHosts(),
				Mirrors:      out,
			}, nil
		})
}
