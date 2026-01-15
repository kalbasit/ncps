package nixcacheindex_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nixcacheindex"
)

func TestParseHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    *big.Int
		wantErr bool
	}{
		{
			name:  "Zero hash",
			input: "00000000000000000000000000000000",
			want:  big.NewInt(0),
		},
		{
			name:  "One (last bit set)",
			input: "00000000000000000000000000000001",
			want:  big.NewInt(1),
		},
		{
			// RFC Example: "100...000" maps to 2^155
			// First char '1' (value 1) is most significant 5 bits
			name:  "2^155 (first bit set)",
			input: "10000000000000000000000000000000",
			want:  new(big.Int).Exp(big.NewInt(2), big.NewInt(155), nil),
		},
		{
			// RFC Example: "010...000" maps to 2^150
			// Second char '1' (value 1) shifted left by (32-2)*5 = 30*5 = 150
			name:  "2^150 (second char set)",
			input: "01000000000000000000000000000000",
			want:  new(big.Int).Exp(big.NewInt(2), big.NewInt(150), nil),
		},
		{
			// RFC Example had a typo saying g=16, but g is 15 in the 0-indexed alphabet.
			// 0-9 (10), a-d (4), f (1), g (1) -> 10+4+1 = 15.
			name:  "Max single char (g)",
			input: "g0000000000000000000000000000000",
			want:  new(big.Int).Mul(big.NewInt(15), new(big.Int).Exp(big.NewInt(2), big.NewInt(155), nil)),
		},
		{
			// Max value: all z's
			name:  "Max value",
			input: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			want:  new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(160), nil), big.NewInt(1)),
		},
		{
			name:    "Invalid length (short)",
			input:   "000",
			wantErr: true,
		},
		{
			name:    "Invalid length (long)",
			input:   "000000000000000000000000000000000",
			wantErr: true,
		},
		{
			name:    "Invalid character",
			input:   "0000000000000000000000000000000e", // 'e' is not in alphabet
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := nixcacheindex.ParseHash(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 0, tt.want.Cmp(got), "expected %s, got %s", tt.want, got)

				// Verify round trip
				formatted := nixcacheindex.FormatHash(got)
				assert.Equal(t, tt.input, formatted, "FormatHash mismatch")
			}
		})
	}
}

func TestFormatHash(t *testing.T) {
	t.Parallel()

	// Focus on round-trip property for random big ints within range
	// But since we covered round trip in TestParseHash, we just add explicit nil check
	assert.Equal(t, "00000000000000000000000000000000", nixcacheindex.FormatHash(nil))
}
