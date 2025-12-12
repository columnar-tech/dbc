package internal

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDbcConfigDirBasicTest(t *testing.T) {
	dir, err := GetDbcConfigDir()
	require.NoError(t, err)
	assert.NotEmpty(t, dir)
	assert.True(t, filepath.IsAbs(dir), "should return absolute path")
}

func TestGetDbcConfigDirCapitalization(t *testing.T) {
	dir, err := GetDbcConfigDir()
	require.NoError(t, err)
	assert.NotEmpty(t, dir)

	parent := filepath.Dir(dir)
	orgName := filepath.Base(parent)

	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		assert.Equal(t, "Columnar", orgName)
	} else {
		assert.Equal(t, "columnar", orgName)
	}
}

func TestGetDbcConfigDirDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping macOS-specific test")
	}
	dir, err := GetDbcConfigDir()
	require.NoError(t, err)
	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, "Library/Application Support/Columnar/dbc"), dir)
}

func TestGetDbcConfigDirLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping Linux-specific test")
	}
	dir, err := GetDbcConfigDir()
	require.NoError(t, err)
	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, ".config/columnar/dbc"), dir)
}

func TestGetDbcConfigDirWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("skipping Windows-specific test")
	}
	dir, err := GetDbcConfigDir()
	require.NoError(t, err)
	appData := os.Getenv("AppData")
	assert.Equal(t, filepath.Join(appData, "Columnar", "dbc"), dir)
}
