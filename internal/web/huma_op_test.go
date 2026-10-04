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
