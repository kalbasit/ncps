package narinfo_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/narinfo"
)

func TestValidateHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		hash      string
		shouldErr bool
	}{
		// Valid Nix32 hashes (32 characters from the allowed alphabet)
		{
			name:      "valid hash with all allowed characters",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc68f",
			shouldErr: false,
		},
		{
			name:      "valid hash with numbers",
			hash:      "01234567890123456789012345678901",
			shouldErr: false,
		},
		{
			name:      "valid hash with mixed characters",
			hash:      "abcdfghijklmnpqrsvwxyzabcdfghijk",
			shouldErr: false,
		},

		// Invalid: contains forbidden letters (e, o, u, t)
		{
			name:      "invalid hash contains 'e'",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc68e",
			shouldErr: true,
		},
		{
			name:      "invalid hash contains 'o'",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc68o",
			shouldErr: true,
		},
		{
			name:      "invalid hash contains 'u'",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc68u",
			shouldErr: true,
		},
		{
			name:      "invalid hash contains 't'",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc68t",
			shouldErr: true,
		},

		// Invalid: contains uppercase letters
		{
			name:      "invalid hash contains uppercase",
			hash:      "N5glp21rsz314qssw9fbvfswgy3kc68f",
			shouldErr: true,
		},
		{
			name:      "invalid hash all uppercase",
			hash:      "N5GLP21RSZ314QSSW9FBVFSWGY3KC68F",
			shouldErr: true,
		},

		// Invalid: contains special characters
		{
			name:      "invalid hash with exclamation mark",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc68!",
			shouldErr: true,
		},
		{
			name:      "invalid hash with hyphen",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc-8f",
			shouldErr: true,
		},
		{
			name:      "invalid hash with underscore",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc_8f",
			shouldErr: true,
		},
		{
			name:      "invalid hash with space",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc 8f",
			shouldErr: true,
		},

		// Invalid: wrong length
		{
			name:      "invalid hash too short",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc68",
			shouldErr: true,
		},
		{
			name:      "invalid hash too long",
			hash:      "n5glp21rsz314qssw9fbvfswgy3kc68ff",
			shouldErr: true,
		},

		// Invalid: empty string
		{
			name:      "invalid hash empty string",
			hash:      "",
			shouldErr: true,
		},

		// Invalid: only one character
		{
			name:      "invalid hash single character",
			hash:      "a",
			shouldErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := narinfo.ValidateHash(test.hash)
			if test.shouldErr {
				assert.ErrorIs(t, err, narinfo.ErrInvalidHash)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
