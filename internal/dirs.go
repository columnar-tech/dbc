package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Get a platform-specific config dir for reading and writing dbc config files
// and credentails
func GetDbcConfigDir() (string, error) {
	userdir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get dbc configuration directory: %v", err)
	}

	orgDirName := "columnar"
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		orgDirName = "Columnar"
	}
	dbcDirName := "dbc"

	return filepath.Join(userdir, orgDirName, dbcDirName), nil
}
