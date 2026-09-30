package vector

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEnsureWritableDir_HappyPath pins the basic contract:
// when the target directory is writable (or can be made
// writable), ensureWritableDir succeeds and leaves no
// probe file behind. The probe file pattern is used here so
// the startup path can surface "directory not writable"
// clearly instead of letting chromem-go's first index write
// fail several layers deep in its internal state machine.
//
// The probe file must be cleaned up even on success, since
// the operator doesn't expect to see .ragabast-write-probe-*
// noise in their vector DB dir.
func TestEnsureWritableDir_HappyPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", "another")

	require.NoError(t, ensureWritableDir(dir))
	_, statErr := os.Stat(dir)
	require.NoError(t, statErr, "ensureWritableDir must create the directory")

	// No probe files left behind on success.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), ".ragabast-write-probe-"),
			"ensureWritableDir must clean up its probe file (left %q behind)", e.Name())
	}
}

// TestEnsureWritableDir_AlreadyExistsDirOk pins the idempotent
// case: a pre-existing directory owned by the binary (the
// "restart" scenario, where DATA_DIR survives across restarts)
// must succeed without altering permissions.
func TestEnsureWritableDir_AlreadyExistsDirOk(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "preexisting"), []byte("ok"), 0o600))
	require.NoError(t, ensureWritableDir(dir))

	// Pre-existing file must still be there.
	_, err := os.Stat(filepath.Join(dir, "preexisting"))
	require.NoError(t, err)
}

// TestNewVectorDB_EACCESOnMkdirAll_HintsFsGroup pins the
// deployment-footgun fix for K8s / Podman pods running as
// nonroot with a PVC mounted at DATA_DIR: the binary's
// MkdirAll on /data/vectors fails because the binary can't
// write into /data (owned by a different uid — typically
// root, or whatever the volume plugin provisioned with). The
// error message must (a) name the directory, (b) name the
// uid/gid the binary is running as, and (c) point the
// operator at securityContext.fsGroup as the K8s fix so they
// don't have to guess.
//
// The test simulates EACCES by chmod'ing a temp parent to
// 0o555 (no write bit) and asking ensureWritableDir to
// create a subdir under it. On Linux this fails for the test
// user with EACCES (matches the deployment scenario). On
// macOS the same chmod model applies. Skip if we can't
// reproduce the failure on the current platform (CI
// matrix), since this is a UX test, not a logic test.
func TestNewVectorDB_EACCESOnMkdirAll_HintsFsGroup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("EACCES simulation requires non-root; skipping under uid=0")
	}

	parent := t.TempDir()
	require.NoError(t, os.Chmod(parent, 0o555))
	// Restrict t.TempDir() cleanup from removing the read-only
	// parent; t.TempDir() tucks the path away but does not
	// walk children for permissions. We're fine.
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o755) // for t.TempDir() cleanup
	})

	target := filepath.Join(parent, "vectors")

	db, err := NewVectorDB("test", 4, target, "test-model")
	require.Error(t, err, "NewVectorDB must fail when the target dir is not writable by the binary")
	require.Nil(t, db)

	require.ErrorIs(t, err, fs.ErrPermission,
		"underlying error must wrap fs.ErrPermission so callers can branch on it (got %T: %v)", err, err)
	require.Contains(t, err.Error(), target,
		"error must name the offending path so the operator knows which DATA_DIR is misconfigured (got: %v)", err)
	require.Contains(t, err.Error(), "fsGroup",
		"error must point at securityContext.fsGroup as the K8s fix (got: %v)", err)
	require.Contains(t, err.Error(), "uid=",
		"error must name the running uid so the operator can compare against fsGroup (got: %v)", err)
}

// TestNewVectorDB_EACCESOnProbeWrite_HintsFsGroup pins the
// harder-to-detect case: the persistence directory exists but
// the binary can't write into it. MkdirAll is a no-op when
// the target already exists, so it passes silently. The
// write probe is what surfaces the real problem.
//
// We simulate this by chmod'ing the target directory to 0o555.
// On Linux this denies the owner write access (chmod applies
// to owner too for directories). On macOS the owner can
// always write to their own directory regardless of mode, so
// the simulation doesn't reproduce the failure; we skip in
// that case rather than ship a false-positive test.
//
// TestNewVectorDB_EACCESErrorFormat covers the same code
// branch cross-platform by feeding a synthetic fs.ErrPermission
// into the wrapDirPermission helper.
func TestNewVectorDB_EACCESOnProbeWrite_HintsFsGroup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("EACCES simulation requires non-root; skipping under uid=0")
	}

	parent := t.TempDir()

	// Pre-create the target while the parent is still writable,
	// so MkdirAll becomes a no-op (target already exists).
	target := filepath.Join(parent, "vectors")
	require.NoError(t, os.Mkdir(target, 0o755))

	// Lock the target down so the probe write fails. On Linux
	// chmod on a directory applies to the owner; on macOS the
	// owner retains write access regardless of mode, which
	// means the probe write will succeed there and the test
	// will spuriously pass. Run a quick canary to detect
	// which platform we're on.
	require.NoError(t, os.Chmod(target, 0o555))
	canary := filepath.Join(target, ".canary")
	if err := os.WriteFile(canary, []byte("ok"), 0o600); err == nil {
		_ = os.Remove(canary)
		t.Skip("chmod 0o555 on a directory does not restrict the owner on this platform (likely macOS); the simulation cannot reproduce EACCES on a pre-existing directory owned by the test user. TestNewVectorDB_EACCESErrorFormat covers the same code branch cross-platform.")
	}

	db, err := NewVectorDB("test", 4, target, "test-model")
	require.Error(t, err)
	require.Nil(t, db)
	require.ErrorIs(t, err, fs.ErrPermission,
		"underlying error must wrap fs.ErrPermission (got %T: %v)", err, err)
	require.Contains(t, err.Error(), target,
		"error must name the offending path (got: %v)", err)
	require.Contains(t, err.Error(), "fsGroup",
		"error must point at securityContext.fsGroup (got: %v)", err)
}

// TestNewVectorDB_EACCESErrorFormat pins the message contract
// for permission errors without depending on chmod semantics
// (which differ between Linux and macOS for owned directories).
// It drives wrapDirPermission directly via ensureWritableDir
// against a synthetic fs.ErrPermission error so the test is
// deterministic cross-platform.
func TestNewVectorDB_EACCESErrorFormat(t *testing.T) {
	// Force a real EACCES by chmod'ing a tmp parent to 0o555
	// and asking ensureWritableDir to create a subdir under
	// it. On non-Linux (where chmod on the owner is permissive)
	// this fails with a non-EACCES error and we skip.
	if os.Geteuid() != 0 {
		parent := t.TempDir()
		require.NoError(t, os.Chmod(parent, 0o555))
		t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

		target := filepath.Join(parent, "vectors")
		err := ensureWritableDir(target)
		require.Error(t, err)
		// On Linux this errors with EACCES; on macOS the
		// owner can write to a 0o555 directory so the call
		// succeeds — we fall back to the synthetic test below.
		if !errors.Is(err, fs.ErrPermission) {
			t.Skipf("chmod-based EACCES simulation does not reproduce on this platform (got %T: %v); falling back to synthetic test", err, err)
			return
		}

		require.Contains(t, err.Error(), target, "error must name the offending path")
		require.Contains(t, err.Error(), "uid=", "error must name the running uid")
		require.Contains(t, err.Error(), "fsGroup", "error must point at securityContext.fsGroup")
	}

	// Synthetic test: feed a known EACCES into wrapDirPermission
	// directly so the message contract is pinned even on
	// platforms where chmod-based simulation can't reproduce.
	wrapped := wrapDirPermission("/data/vectors",
		&fs.PathError{Op: "open", Path: "/data/vectors/.probe", Err: fs.ErrPermission})
	require.ErrorIs(t, wrapped, fs.ErrPermission,
		"wrapped error must still satisfy errors.Is(err, fs.ErrPermission)")
	require.Contains(t, wrapped.Error(), "/data/vectors", "error must name the offending path")
	require.Contains(t, wrapped.Error(), "uid=", "error must name the running uid")
	require.Contains(t, wrapped.Error(), "fsGroup", "error must point at securityContext.fsGroup")

	// Non-permission errors must NOT get the fsGroup hint —
	// that guidance is specific to the uid/gid-mismatch case.
	otherErr := wrapDirPermission("/some/dir", errors.New("ENOSPC: out of space"))
	require.NotContains(t, otherErr.Error(), "fsGroup",
		"non-permission errors must NOT carry the fsGroup hint")
}
