package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stringReader returns a *strings.Reader from a string,
// keeping the test bodies short. A real POST would use a
// body closer, but for unit tests this is enough.
func stringReader(s string) *strings.Reader {
	return strings.NewReader(s)
}

// TestRegisterPOST_TypedHandlerInvoked pins the typed-wrapper
// contract: a handler registered with registerPOST receives a
// concrete *Request type, returns a concrete *Response type,
// and the wire shape matches what huma would have produced
// from the untyped anonymous-struct form.
func TestRegisterPOST_TypedHandlerInvoked(t *testing.T) {
	_, api := humatest.New(t)

	type greetReq struct {
		Name string `json:"name" required:"true"`
	}
	type greetResp struct {
		Greeting string `json:"greeting"`
	}

	var called bool
	var gotName string
	registerPOST[greetReq, greetResp](api, humaOpPOST("/greet", "Greet someone"),
		func(_ context.Context, req *greetReq) (*greetResp, error) {
			called = true
			gotName = req.Name
			return &greetResp{Greeting: "hello, " + req.Name}, nil
		})

	// Drive the request through humatest's recorder.
	resp := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/greet",
		stringReader(`{"name":"world"}`))
	req.Header.Set("Content-Type", "application/json")
	api.Adapter().ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.True(t, called, "handler should have been invoked")
	assert.Equal(t, "world", gotName, "handler should have received the parsed body")
	assert.Contains(t, resp.Body.String(), `"greeting":"hello, world"`)
}

// TestRegisterGET_TypedHandlerInvoked pins the typed-wrapper
// contract for the GET (no-body) case. A no-body GET should
// work with a *Response-only signature.
func TestRegisterGET_TypedHandlerInvoked(t *testing.T) {
	_, api := humatest.New(t)

	type pingResp struct {
		Pong string `json:"pong"`
	}

	var called bool
	registerGET[pingResp](api, humaOpGET("/ping", "Health-style ping"),
		func(_ context.Context) (*pingResp, error) {
			called = true
			return &pingResp{Pong: "yes"}, nil
		})

	resp := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ping", nil)
	api.Adapter().ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.True(t, called, "handler should have been invoked")
	assert.Contains(t, resp.Body.String(), `"pong":"yes"`)
}

// TestRegisterWithInput_PathParam pins the typed-wrapper
// contract for endpoints whose input is carried in path or
// query parameters (DELETE /x/{id}, GET /x/{id}, GET /x?y=z).
// The handler signature is (ctx, *In) → (*Out, error) and
// huma parses In's path/query/header/cookie tags into the
// request shape — no Body wrapper.
func TestRegisterWithInput_PathParam(t *testing.T) {
	_, api := humatest.New(t)

	type delReq struct {
		DocumentID string `path:"document_id"`
	}
	type delResp struct {
		DeletedID string `json:"deleted_id"`
	}

	var called bool
	var gotID string
	registerWithInput[delReq, delResp](api, humaOpDELETE("/items/{document_id}", "Delete an item"),
		func(_ context.Context, in *delReq) (*delResp, error) {
			called = true
			gotID = in.DocumentID
			return &delResp{DeletedID: in.DocumentID}, nil
		})

	resp := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/items/abc-123", nil)
	api.Adapter().ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.True(t, called, "handler should have been invoked")
	assert.Equal(t, "abc-123", gotID, "handler should have received the path param")
	assert.Contains(t, resp.Body.String(), `"deleted_id":"abc-123"`)
}

// TestRegisterWithInput_QueryParam pins the typed-wrapper
// contract for endpoints that take query parameters (no
// path param, no body). The In struct uses query:"…"
// tags; huma parses them from the URL.
func TestRegisterWithInput_QueryParam(t *testing.T) {
	_, api := humatest.New(t)

	type listReq struct {
		Limit  int `query:"limit"`
		Offset int `query:"offset"`
	}
	type listResp struct {
		Count int `json:"count"`
	}

	var called bool
	var gotLimit, gotOffset int
	registerWithInput[listReq, listResp](api, humaOpGET("/list", "List things"),
		func(_ context.Context, in *listReq) (*listResp, error) {
			called = true
			gotLimit = in.Limit
			gotOffset = in.Offset
			return &listResp{Count: in.Limit + in.Offset}, nil
		})

	resp := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/list?limit=10&offset=5", nil)
	api.Adapter().ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.True(t, called, "handler should have been invoked")
	assert.Equal(t, 10, gotLimit)
	assert.Equal(t, 5, gotOffset)
	assert.Contains(t, resp.Body.String(), `"count":15`)
}

// TestRegisterPOST_ValidationError pins the contract: when
// the request body fails validation (required field missing),
// huma returns its standard 422 and the handler is NOT called.
func TestRegisterPOST_ValidationError(t *testing.T) {
	_, api := humatest.New(t)

	type needReq struct {
		Name string `json:"name" required:"true"`
	}
	type okResp struct {
		OK bool `json:"ok"`
	}

	var called bool
	registerPOST[needReq, okResp](api, humaOpPOST("/need", "Need a name"),
		func(_ context.Context, _ *needReq) (*okResp, error) {
			called = true
			return &okResp{OK: true}, nil
		})

	resp := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/need",
		stringReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	api.Adapter().ServeHTTP(resp, req)

	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code,
		"missing required field must produce a 422")
	assert.False(t, called, "handler must not be invoked when validation fails")
}
