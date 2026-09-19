package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVectorReset_ForceWipesPersistenceDir pins the recovery path
// for the "vectors must have the same length" failure mode: an
// operator can force-wipe the on-disk vector store so a fresh
// `ragabast ingest` rebuilds it from a single embedding shape.
//
// The reset must:
//   - Default to the persistence_dir from config (resolved
//     against CWD).
//   - When --force is set, remove every file under that dir
//     without prompting.
//   - When --force is NOT set, refuse to run (the command
//     would otherwise silently delete a production corpus;
//     the operator must opt in).
func TestVectorReset_ForceWipesPersistenceDir(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	// Lay down a fake vector store: a directory with a file
	// inside it that the reset must delete.
	persistDir := filepath.Join(tmp, "data", "vectors")
	require.NoError(t, os.MkdirAll(persistDir, 0o755))
	staleFile := filepath.Join(persistDir, "stale.chromem")
	require.NoError(t, os.WriteFile(staleFile, []byte("stale"), 0o644))

	// Write a config that points at persistDir.
	cfgPath := filepath.Join(tmp, "config.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"vectordb:\n  persistence_dir: "+persistDir+"\n"+
			"  collection_name: test\n"+
			"  embedding_dimension: 768\n"+
			"ollama:\n  base_url: http://127.0.0.1:8000/v1\n"+
			"  embedding_model: x\n"+
			"  chat_base_url: http://127.0.0.1:8000/v1\n"+
			"  chat_model: x\n"+
			"  timeout: 30s\n"+
			"server:\n  address: 0.0.0.0\n  port: 8080\n"+
			"processing:\n  max_chunk_size: 2000\n  min_chunk_size: 300\n  chunk_overlap: 150\n"+
			"paths:\n  data_dir: "+filepath.Join(tmp, "data")+"\n"+
			"  templates_dir: \"\"\n",
	), 0o644))

	cmd := VectorResetCmd{ConfigOpts: ConfigOpts{Config: cfgPath}, Force: true}
	require.NoError(t, cmd.Run(nil),
		"vector reset --force must succeed against an existing on-disk store")

	// The persistence dir must now be empty (the chromem store
	// is recreated as an empty dir by chromem-go on first Add).
	gone, err := os.Stat(staleFile)
	if err == nil {
		t.Fatalf("expected stale file to be gone, but stat succeeded: %+v", gone)
	}
	require.True(t, os.IsNotExist(err),
		"expected IsNotExist on the stale file after reset, got: %v", err)
}

// TestVectorReset_RequiresForce pins the safety contract: without
// --force the command refuses to run. We do not assert the
// prompt content (kong doesn't surface it through Run), only
// that an error is returned and the dir is untouched.
func TestVectorReset_RequiresForce(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	persistDir := filepath.Join(tmp, "data", "vectors")
	require.NoError(t, os.MkdirAll(persistDir, 0o755))
	keepFile := filepath.Join(persistDir, "keep.chromem")
	require.NoError(t, os.WriteFile(keepFile, []byte("keep me"), 0o644))

	cfgPath := filepath.Join(tmp, "config.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"vectordb:\n  persistence_dir: "+persistDir+"\n"+
			"  collection_name: test\n"+
			"  embedding_dimension: 768\n"+
			"ollama:\n  base_url: http://127.0.0.1:8000/v1\n"+
			"  embedding_model: x\n"+
			"  chat_base_url: http://127.0.0.1:8000/v1\n"+
			"  chat_model: x\n"+
			"  timeout: 30s\n"+
			"server:\n  address: 0.0.0.0\n  port: 8080\n"+
			"processing:\n  max_chunk_size: 2000\n  min_chunk_size: 300\n  chunk_overlap: 150\n"+
			"paths:\n  data_dir: "+filepath.Join(tmp, "data")+"\n"+
			"  templates_dir: \"\"\n",
	), 0o644))

	cmd := VectorResetCmd{ConfigOpts: ConfigOpts{Config: cfgPath}, Force: false}
	require.Error(t, cmd.Run(nil),
		"vector reset without --force must refuse to run")

	// The file must still be there.
	_, err := os.Stat(keepFile)
	require.NoError(t, err, "without --force the file must not be touched")
}

// TestVectorReset_MissingPersistenceDirIsClear pins that
// pointing at a nonexistent dir produces a clear error (not a
// silent success, not a panic).
func TestVectorReset_MissingPersistenceDirIsClear(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfgPath := filepath.Join(tmp, "config.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"vectordb:\n  persistence_dir: "+filepath.Join(tmp, "does-not-exist")+"\n"+
			"  collection_name: test\n"+
			"  embedding_dimension: 768\n"+
			"ollama:\n  base_url: http://127.0.0.1:8000/v1\n"+
			"  embedding_model: x\n"+
			"  chat_base_url: http://127.0.0.1:8000/v1\n"+
			"  chat_model: x\n"+
			"  timeout: 30s\n"+
			"server:\n  address: 0.0.0.0\n  port: 8080\n"+
			"processing:\n  max_chunk_size: 2000\n  min_chunk_size: 300\n  chunk_overlap: 150\n"+
			"paths:\n  data_dir: "+filepath.Join(tmp, "data")+"\n"+
			"  templates_dir: \"\"\n",
	), 0o644))

	cmd := VectorResetCmd{ConfigOpts: ConfigOpts{Config: cfgPath}, Force: true}
	require.Error(t, cmd.Run(nil),
		"vector reset against a nonexistent persistence dir must error, not silently succeed")
}
