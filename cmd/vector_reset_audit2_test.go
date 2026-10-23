package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeResetConfig writes a config file pointing at persistDir.
// An empty persistDir is written verbatim (no persistence_dir
// key at all), which is how the "refusing to wipe a misconfigured
// path" branch is reached.
//
// An empty data_dir is written alongside an empty persistDir:
// config.Load re-derives persistence_dir from data_dir whenever
// persistence_dir is still at its default, so both must be blank
// for the path to actually come out empty.
func writeResetConfig(t *testing.T, dir, persistDir string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.yml")
	body := "vectordb:\n" +
		"  collection_name: test\n" +
		"  embedding_dimension: 768\n"
	if persistDir != "" {
		body += "  persistence_dir: " + persistDir + "\n"
	} else {
		body += "  persistence_dir: \"\"\n"
	}
	body += "ollama:\n  base_url: http://127.0.0.1:8000/v1\n" +
		"  embedding_model: x\n" +
		"  chat_base_url: http://127.0.0.1:8000/v1\n" +
		"  chat_model: x\n" +
		"  timeout: 30s\n" +
		"server:\n  address: 0.0.0.0\n  port: 8080\n" +
		"processing:\n  max_chunk_size: 2000\n  min_chunk_size: 300\n  chunk_overlap: 150\n" +
		"paths:\n"
	if persistDir == "" {
		body += "  data_dir: \"\"\n"
	}
	require.NoError(t, os.WriteFile(cfgPath, []byte(body), 0o644))
	return cfgPath
}

// TestVectorReset_EmptyPersistenceDirIsRefused pins audit
// finding 11: the command must refuse to wipe a misconfigured
// path rather than calling os.RemoveAll("") — which on some
// platforms is a no-op and on others is a very bad idea.
//
// The branch was uncovered: cmd's overall coverage was 34.5% and
// this guard never ran.
func TestVectorReset_EmptyPersistenceDirIsRefused(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfgPath := writeResetConfig(t, tmp, "")

	cmd := VectorResetCmd{ConfigOpts: ConfigOpts{Config: cfgPath}, Force: true}
	err := cmd.Run(nil)
	require.Error(t, err,
		"vector reset must refuse to run when persistence_dir is empty")
	require.Contains(t, err.Error(), "persistence_dir",
		"the error must name the knob that is misconfigured")
}

// TestVectorReset_PersistencePathIsNotADirectory pins the other
// misconfiguration guard: the configured path exists but is a
// regular file, so wiping it would delete a file the operator
// never meant to touch.
func TestVectorReset_PersistencePathIsNotADirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	notADir := filepath.Join(tmp, "vectors-file")
	require.NoError(t, os.WriteFile(notADir, []byte("not a directory"), 0o644))

	cfgPath := writeResetConfig(t, tmp, notADir)

	cmd := VectorResetCmd{ConfigOpts: ConfigOpts{Config: cfgPath}, Force: true}
	err := cmd.Run(nil)
	require.Error(t, err,
		"vector reset must refuse to run when the path is not a directory")

	// The file must survive.
	_, statErr := os.Stat(notADir)
	require.NoError(t, statErr,
		"the refused path must not be touched")
}

// TestVectorReset_MissingConfigIsAnError pins that a config file
// that does not exist produces an error rather than silently
// falling back to defaults — the default persistence dir is
// derived from the CWD, so a silent fallback would point the
// wipe at an unrelated directory.
func TestVectorReset_MissingConfigIsAnError(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfgPath := filepath.Join(tmp, "no-such-config.yml")

	cmd := VectorResetCmd{ConfigOpts: ConfigOpts{Config: cfgPath}, Force: true}
	require.Error(t, cmd.Run(nil),
		"a missing config file must be an error, not a silent default")
}

// TestVectorReset_WipesRelocatedKeywordIndex pins that a
// keyword index living OUTSIDE the persistence dir is wiped too.
//
// Operators who split the stores — the documented setup for NFS,
// where bleve's mmap reads perform badly — point
// vectordb.keyword_index_dir at a local SSD. That copy is not
// under the persistence dir, so removing the persistence dir
// leaves it behind holding chunks for documents whose vectors
// are gone: search results referencing deleted documents.
//
// The README promised that `vector reset` "wipes both stores in
// one go"; that promise was false for this configuration.
func TestVectorReset_WipesRelocatedKeywordIndex(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	persistDir := filepath.Join(tmp, "data", "vectors")
	kwDir := filepath.Join(tmp, "local-ssd", "search")
	require.NoError(t, os.MkdirAll(persistDir, 0o755))
	require.NoError(t, os.MkdirAll(kwDir, 0o755))

	kwFile := filepath.Join(kwDir, "stale.bleve")
	require.NoError(t, os.WriteFile(kwFile, []byte("stale"), 0o644))

	cfgPath := filepath.Join(tmp, "config.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"vectordb:\n  persistence_dir: "+persistDir+"\n"+
			"  keyword_index_dir: "+kwDir+"\n"+
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
	require.NoError(t, cmd.Run(nil))

	_, err := os.Stat(kwFile)
	require.True(t, os.IsNotExist(err),
		"a keyword index outside the persistence dir must be wiped too")
}

// TestVectorReset_RecreatesEmptyDir pins that the reset leaves
// the directory in place (empty), not removed. The chromem-go
// store fails with "no such directory" on the first Add if the
// parent is missing.
func TestVectorReset_RecreatesEmptyDir(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	persistDir := filepath.Join(tmp, "data", "vectors")
	require.NoError(t, os.MkdirAll(persistDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(persistDir, "stale.chromem"), []byte("x"), 0o644))

	cfgPath := writeResetConfig(t, tmp, persistDir)

	cmd := VectorResetCmd{ConfigOpts: ConfigOpts{Config: cfgPath}, Force: true}
	require.NoError(t, cmd.Run(nil))

	info, err := os.Stat(persistDir)
	require.NoError(t, err, "the persistence dir must be recreated, not left missing")
	require.True(t, info.IsDir(), "the persistence dir must be a directory")

	entries, err := os.ReadDir(persistDir)
	require.NoError(t, err)
	require.Empty(t, entries, "the recreated dir must be empty")
}
