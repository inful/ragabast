package vector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOllamaLLMClient_GenerateWithOptions_SendsTemperature(t *testing.T) {
	t.Parallel()

	errCh := make(chan error, 10)
	gotTempCh := make(chan float64, 1)
	sendErr := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}

	var got struct {
		Options map[string]any `json:"options"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"response":"ok","done":true}`))
		}()

		if r.Method != http.MethodPost {
			sendErr(fmt.Errorf("unexpected method: %s", r.Method))
			return
		}
		if r.URL.Path != "/api/generate" {
			sendErr(fmt.Errorf("unexpected path: %s", r.URL.Path))
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			sendErr(fmt.Errorf("read body: %w", err))
			return
		}

		if err := json.Unmarshal(body, &got); err != nil {
			sendErr(fmt.Errorf("unmarshal body: %w", err))
			return
		}

		if got.Options == nil {
			sendErr(errors.New("missing options"))
			return
		}

		v, ok := got.Options["temperature"]
		if !ok {
			sendErr(errors.New("missing temperature option"))
			return
		}

		f, ok := v.(float64)
		if !ok {
			sendErr(fmt.Errorf("temperature is not float64: %T", v))
			return
		}
		gotTempCh <- f
	}))
	t.Cleanup(srv.Close)

	client := NewOllamaLLMClientWithTimeout(srv.URL, "some-model", 5*time.Second)

	ctx := context.Background()
	out, err := client.GenerateWithOptions(ctx, "hello", map[string]any{"temperature": 0.12})
	require.NoError(t, err)
	require.Equal(t, "ok", out)

	select {
	case err := <-errCh:
		require.NoError(t, err)
	default:
	}

	select {
	case gotTemp := <-gotTempCh:
		require.InDelta(t, 0.12, gotTemp, 1e-9)
	default:
		t.Fatal("did not observe temperature in request")
	}
}
