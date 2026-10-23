package vector

import (
	"fmt"
	"sync"
	"testing"
)

// TestLockForDoc_ReusesTheSameMutex pins the core contract of
// the per-doc lock: one mutex per doc.ID, reused across calls.
//
// Audit finding 5 flagged that the map is never pruned. The
// bound is documented rather than enforced — a mutex cannot be
// safely removed from the map while another goroutine may be
// holding it (the classic hazard of deleting a lock someone
// else is waiting on). So the map grows with the number of
// distinct doc.IDs ingested in this process's lifetime.
//
// What matters is that the growth is bounded by DISTINCT IDs,
// not by the number of calls: re-ingesting the same document
// must not add an entry.
func TestLockForDoc_ReusesTheSameMutex(t *testing.T) {
	t.Parallel()

	op := &VectorOperations{}
	mu := op.lockForDoc("doc-1")

	for range 1000 {
		if got := op.lockForDoc("doc-1"); got != mu {
			t.Fatal("lockForDoc must return the same mutex for the same doc.ID")
		}
	}

	op.ingestMu.Lock()
	entries := len(op.ingestMuByID)
	op.ingestMu.Unlock()

	if entries != 1 {
		t.Fatalf("re-ingesting the same doc must not grow the map, got %d entries", entries)
	}
}

// TestLockForDoc_MapIsBoundedByDistinctIDs pins the documented
// bound of audit finding 5: the map grows with the number of
// DISTINCT doc.IDs ingested in this process, and with nothing
// else.
//
// This is what makes the un-pruned map acceptable. The cost per
// entry is the ID string plus a mutex — roughly 60 bytes for a
// UUID — so a corpus of N documents costs N times that, once,
// for the life of the process. It is not a per-request or
// per-chunk cost, and it does not grow as documents are
// re-ingested.
func TestLockForDoc_MapIsBoundedByDistinctIDs(t *testing.T) {
	t.Parallel()

	op := &VectorOperations{}

	const distinct = 100
	const repeats = 10
	for i := range distinct {
		id := fmt.Sprintf("doc-%d", i)
		for range repeats {
			op.lockForDoc(id)
		}
	}

	op.ingestMu.Lock()
	entries := len(op.ingestMuByID)
	op.ingestMu.Unlock()

	if entries != distinct {
		t.Fatalf("map must hold one entry per distinct doc.ID: want %d, got %d", distinct, entries)
	}
}

// TestLockForDoc_ConcurrentCallersShareOneMutex pins that
// concurrent callers for the same doc.ID all get the SAME mutex.
// If two callers could get different mutexes, the per-doc
// serialization would silently stop working — which is the
// whole point of the map.
//
// This is the property that makes the un-pruned map safe: the
// mutex identity is stable for the life of the entry, so a
// waiter that captured the pointer keeps waiting on the same
// lock the holder will release.
func TestLockForDoc_ConcurrentCallersShareOneMutex(t *testing.T) {
	t.Parallel()

	op := &VectorOperations{}
	const callers = 32

	var wg sync.WaitGroup
	seen := make([]*sync.Mutex, callers)
	for i := range seen {
		wg.Go(func() {
			seen[i] = op.lockForDoc("doc-1")
		})
	}
	wg.Wait()

	for i, mu := range seen {
		if mu != seen[0] {
			t.Fatalf("caller %d got a different mutex than caller 0", i)
		}
	}
}
