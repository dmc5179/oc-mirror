package fileutil

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

// CreateSymlink creates a symlink with Windows fallback to file copy.
// On Unix systems, it creates a symbolic link. On Windows, it attempts to
// create a symlink but falls back to copying the file if symlink creation
// fails (which requires Developer Mode or admin privileges on Windows).
func CreateSymlink(target, link string) error {
	err := os.Symlink(target, link)
	if err != nil && runtime.GOOS == "windows" {
		// Symlink failed on Windows, copy file instead
		return copyFile(target, link)
	}
	return err
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy: %w", err)
	}

	return destFile.Sync()
}
