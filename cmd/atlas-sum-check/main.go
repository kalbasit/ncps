// Command atlas-sum-check verifies that the on-disk `atlas.sum` file in
// each `migrations/<dialect>/` directory matches the directory contents.
//
// Atlas uses the integrity file to detect tampering and to validate that a
// migrations tree is internally consistent before replaying it. This check
// gates that property in CI: it reads each dialect's directory via Atlas's
// own `sqltool.NewGooseDir` helper, recomputes the checksum, and compares
// the result against the on-disk `atlas.sum`. Any drift fails the build.
//
// Usage:
//
//	atlas-sum-check --root .
//
// The binary exits non-zero on the first mismatch and prints a diagnostic
// pointing at the offending directory.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"ariga.io/atlas/sql/sqltool"

	atlasmigrate "ariga.io/atlas/sql/migrate"
)

// errSumMismatch is returned when a directory's recomputed checksum does
// not match its on-disk atlas.sum. err113 wants a sentinel error, so this
// is the canonical one for the lint pass.
var errSumMismatch = errors.New("atlas.sum mismatch")

func main() {
	root := flag.String("root", ".", "repository root (contains migrations/<dialect>/)")

	flag.Parse()

	dialects := []string{"sqlite", "postgres", "mysql"}

	var failed int

	for _, d := range dialects {
		dir := filepath.Join(*root, "migrations", d)
		if err := checkDir(dir); err != nil {
			fmt.Fprintf(os.Stderr, "[FAIL] %s: %v\n", dir, err)

			failed++

			continue
		}

		fmt.Printf("[PASS] %s: atlas.sum matches directory contents\n", dir)
	}

	if failed > 0 {
		log.Fatalf("atlas-sum-check: %d dialect(s) failed", failed)
	}
}

func checkDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	if !info.IsDir() {
		//nolint:err113 // diagnostic
		return fmt.Errorf("%s is not a directory", dir)
	}

	gdir, err := sqltool.NewGooseDir(dir)
	if err != nil {
		return fmt.Errorf("NewGooseDir: %w", err)
	}

	got, err := gdir.Checksum()
	if err != nil {
		return fmt.Errorf("checksum: %w", err)
	}

	// Serialize the recomputed sum to atlas.sum's on-disk format so we can
	// compare against the on-disk file byte-for-byte.
	gotBytes, err := got.MarshalText()
	if err != nil {
		return fmt.Errorf("marshal recomputed sum: %w", err)
	}

	sumPath := filepath.Join(dir, atlasmigrate.HashFileName)

	wantBytes, err := os.ReadFile(sumPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", sumPath, err)
	}

	if !bytes.Equal(bytes.TrimSpace(gotBytes), bytes.TrimSpace(wantBytes)) {
		return fmt.Errorf("%w: regenerate via `go run ./cmd/generate-migrations`", errSumMismatch)
	}

	return nil
}
