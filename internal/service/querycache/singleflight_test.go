package querycache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCache_GetSingleflightsLoader pins the cache-stampede fix
// (audit finding 3): N concurrent Get calls for the same key
// result in exactly one loader invocation, not N. The previous
// behavior was documented as "loader may run more than once
// for the same key under high contention" — which is the
// thundering-herd shape and was the whole reason the cache
// exists in the first place (to avoid running the embedding
// pipeline N times for one popular query).
//
// The test simulates a slow loader (blocks on a channel) so
// all N concurrent callers race into Get before any of them
// finishes. Without singleflight, every caller would run its
// own loader; with it, exactly one runs.
func TestCache_GetSingleflightsLoader(t *testing.T) {
	t.Parallel()

	c := New[int](16, 0)
	var loadCalls atomic.Int32
	release := make(chan struct{})
	//nolint:unparam // loader returns error to match the production Get signature; this loader never fails, which the test pins as part of the happy path.
	loader := func(_ context.Context) (int, error) {
		loadCalls.Add(1)
		<-release // block until the test releases us
		return 42, nil
	}

	const callers = 16
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			_, _, err := c.Get(context.Background(), "popular", loader)
			if err != nil {
				t.Errorf("Get: %v", err)
			}
		}()
	}

	// Give every goroutine a chance to enter Get.
	// Without a yield here, fast schedulers may serialize the
	// 16 goroutines tightly enough that the first one finishes
	// before the rest arrive — masking the race we want to pin.
	time.Sleep(50 * time.Millisecond)

	close(release) // unblock the loader exactly once
	wg.Wait()

	if got := loadCalls.Load(); got != 1 {
		t.Fatalf("expected loader to run exactly once under 16-way concurrency, ran %d times", got)
	}
}

// TestCache_GetErrorDoesntPoison pins that an error from the
// first loader doesn't poison the inflight slot for future
// callers. (singleflight.Group shares errors but the cache
// still has to clean up the inflight entry so the NEXT Get for
// a different key, or a future Get for this key after the
// error, can attempt a fresh load.)
func TestCache_GetErrorDoesntPoison(t *testing.T) {
	t.Parallel()

	c := New[int](16, 0)
	calls := 0
	loader := func(ctx context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, context.DeadlineExceeded
		}
		return 7, nil
	}

	v, _, err := c.Get(context.Background(), "k", loader)
	if err == nil {
		t.Fatalf("first call must return the loader's error")
	}
	_ = v

	// Second call must re-invoke the loader (don't poison).
	_, _, err = c.Get(context.Background(), "k", loader)
	if err != nil {
		t.Fatalf("second call should succeed, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("loader must run twice (once for error, once for success), ran %d", calls)
	}
}
