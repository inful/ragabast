package web

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// humaOpPOST is a small constructor for a POST huma.Operation.
// The wrapper exists so registerPOST callers can pass the
// operation in one expression:
//
//	registerPOST[Req, Resp](api, humaOpPOST("/path", "Summary"),
//	    func(ctx context.Context, req *Req) (*Resp, error) { ... })
//
// The full huma.Operation struct is returned so callers can
// chain additional setters (Description, Tags, DefaultStatus,
// etc.) before passing to registerPOST.
func humaOpPOST(path, summary string) huma.Operation {
	return huma.Operation{
		Method:  "POST",
		Path:    path,
		Summary: summary,
	}
}

// humaOpGET mirrors humaOpPOST for GET endpoints. See
// humaOpPOST for the rationale.
func humaOpGET(path, summary string) huma.Operation {
	return huma.Operation{
		Method:  "GET",
		Path:    path,
		Summary: summary,
	}
}

// registerPOST registers a typed POST handler on api.
//
// Why a thin wrapper around huma.Register: every existing
// register*Operations function in this package defines
// anonymous input/output structs inline (so the
// &struct{ Body X }{...} pattern shows up in every handler).
// The wrapper accepts a concrete input and output type so the
// handler signature reads as the actual API contract:
//
//	registerPOST[Req, Resp](api, humaOpPOST("/x", "X"),
//	    func(ctx, req *Req) (*Resp, error) { ... })
//
// huma's schema generation still works: the wrapper builds
// the anonymous struct{ Body In } / struct{ Body Out } shape
// huma.Register expects, so the generated OpenAPI matches the
// previous hand-rolled version exactly.
func registerPOST[In, Out any](
	api huma.API,
	op huma.Operation,
	handler func(context.Context, *In) (*Out, error),
) {
	huma.Register(api, op, func(ctx context.Context, input *struct {
		Body In
	},
	) (*struct {
		Body Out
	}, error,
	) {
		out, err := handler(ctx, &input.Body)
		if err != nil {
			return nil, err
		}
		return &struct {
			Body Out
		}{Body: *out}, nil
	})
}

// registerGET registers a typed GET handler on api. Mirrors
// registerPOST but for handlers that take no body.
func registerGET[Out any](
	api huma.API,
	op huma.Operation,
	handler func(context.Context) (*Out, error),
) {
	huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*struct {
		Body Out
	}, error,
	) {
		out, err := handler(ctx)
		if err != nil {
			return nil, err
		}
		return &struct {
			Body Out
		}{Body: *out}, nil
	})
}
