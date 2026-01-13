package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigInit_WritesDefaultConfigYML(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cmd := ConfigInitCmd{}
	require.NoError(t, cmd.Run(nil))

	_, err := os.Stat(filepath.Join(tmp, "config.yml"))
	require.NoError(t, err)
}

func TestConfigInit_DoesNotOverwriteWithoutForce(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cmd := ConfigInitCmd{}
	require.NoError(t, cmd.Run(nil))

	err := cmd.Run(nil)
	require.Error(t, err)
}

func TestConfigInit_OverwritesWithForce(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cmd := ConfigInitCmd{}
	require.NoError(t, cmd.Run(nil))

	cmd.Force = true
	require.NoError(t, cmd.Run(nil))
}
