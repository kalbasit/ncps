package helper

import (
	"fmt"
	"os"
)

func EnsureDirWritable(d string) error {
	// Verify the tmp directory is writable before accepting it.
	tmpFile, err := os.CreateTemp(d, "boot_test")
	if err != nil {
		return fmt.Errorf("error verifying tmp directory is writable: %w", err)
	}
	// Defer removal for cleanup, even if Close fails.
	defer os.Remove(tmpFile.Name())

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("error closing temp file during writability check: %w", err)
	}

	return nil
}
