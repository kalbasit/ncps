package testdata

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/nix-community/go-nix/pkg/nixbase32"

	"github.com/kalbasit/ncps/pkg/nar"
)

// GenerateEntry creates an Entry from raw NAR data.
// This is useful for testing with custom/large NARs.
func GenerateEntry(t *testing.T, narData []byte) (Entry, error) {
	t.Helper()

	// Calculate SHA256 hash of the NAR data
	hash := sha256.Sum256(narData)
	narHash := nixbase32.EncodeToString(hash[:])

	// For simplicity, use the same hash for both file and nar
	// In real scenarios, these would be different
	storePath := fmt.Sprintf("/nix/store/%s-generated-test-package", narHash[0:32])
	narInfoHash := narHash[0:32]

	// Create narinfo text (using uncompressed NAR for simplicity in testing)
	narURL := fmt.Sprintf("nar/%s.nar", narHash)
	// Note: Generated entries do not include signatures since we don't have the private key.
	// Tests using these entries should not use public key verification.
	// Use the narinfo hash as a self-reference since this is a generated test package with no dependencies.
	narInfoText := fmt.Sprintf(`StorePath: %s
URL: %s
Compression: none
FileHash: sha256:%s
FileSize: %d
NarHash: sha256:%s
NarSize: %d
References: %s-generated-test`,
		storePath,
		narURL,
		narHash,
		len(narData),
		narHash,
		len(narData),
		narInfoHash,
	)

	return Entry{
		NarInfoHash:    narInfoHash,
		NarInfoPath:    filepath.Join("n", narInfoHash[0:2], narInfoHash+".narinfo"),
		NarInfoText:    narInfoText,
		NarHash:        narHash,
		NarCompression: nar.CompressionTypeNone,
		NarPath:        filepath.Join(narHash[0:1], narHash[0:2], narHash+".nar"),
		NarText:        string(narData),
	}, nil
}
