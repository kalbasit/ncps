package nixcacheindex_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nixcacheindex"
)

func TestJournalRoundTrip(t *testing.T) {
	t.Parallel()

	entries := []nixcacheindex.JournalEntry{
		{Op: nixcacheindex.OpAdd, Hash: "b6gvzjyb2pg0kjfwn6a6llj3k1bq6dwi"},
		{Op: nixcacheindex.OpAdd, Hash: "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6"},
		{Op: nixcacheindex.OpDelete, Hash: "x9y8z7w6v5u4t3s2r1q0p9o8n7m6l5k4"},
	}

	var buf bytes.Buffer

	err := nixcacheindex.WriteJournal(&buf, entries)
	require.NoError(t, err)

	parsed, err := nixcacheindex.ParseJournal(&buf)
	require.NoError(t, err)

	assert.Equal(t, entries, parsed)
}

func TestParseJournal_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "Invalid Op",
			input:   "*b6gvzjyb2pg0kjfwn6a6llj3k1bq6dwi",
			wantErr: "invalid journal operation",
		},
		{
			name:    "Short Line",
			input:   "+short",
			wantErr: "invalid hash length",
		},
		{
			name:    "Long Line",
			input:   "+b6gvzjyb2pg0kjfwn6a6llj3k1bq6dwi1",
			wantErr: "invalid hash length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entries, err := nixcacheindex.ParseJournal(strings.NewReader(tt.input))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, entries)
		})
	}
}

func TestWriteJournal_Errors(t *testing.T) {
	t.Parallel()

	entries := []nixcacheindex.JournalEntry{
		{Op: nixcacheindex.OpAdd, Hash: "short"}, // Invalid hash
	}

	var buf bytes.Buffer

	err := nixcacheindex.WriteJournal(&buf, entries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hash length")
}
