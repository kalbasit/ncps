package nixcacheindex

import (
	"fmt"
	"math/big"
	"strings"
)

const (
	// Alphabet is the Nix Base32 alphabet.
	// Note: 'e', 'o', 'u', 't' are excluded to avoid offensive words.
	Alphabet = "0123456789abcdfghijklmnpqrsvwxyz"

	// HashLength is the length of a Nix store path hash in base32 characters.
	HashLength = 32

	// HashBits is the number of bits in a full store path hash (160).
	HashBits = 160
)

//nolint:gochecknoglobals
var alphabetMap map[rune]int64

//nolint:gochecknoinits
func init() {
	alphabetMap = make(map[rune]int64)
	for i, c := range Alphabet {
		alphabetMap[c] = int64(i)
	}
}

// ParseHash parses a 32-character Nix base32 string into a big.Int.
// The string is interpreted as a Big-Endian 160-bit unsigned integer.
// This means the first character is the most significant.
func ParseHash(s string) (*big.Int, error) {
	if len(s) != HashLength {
		return nil, fmt.Errorf("%w: expected %d, got %d", ErrInvalidHashLength, HashLength, len(s))
	}

	result := new(big.Int)

	for _, char := range s {
		val, ok := alphabetMap[char]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidHashChar, char)
		}

		// result = (result << 5) | val
		result.Lsh(result, 5)
		result.Or(result, big.NewInt(val))
	}

	return result, nil
}

// FormatHash formats a big.Int into a 32-character Nix base32 string.
// The integer is treated as Big-Endian.
func FormatHash(i *big.Int) string {
	if i == nil {
		return strings.Repeat("0", HashLength)
	}

	// Work with a copy since we'll act on it
	n := new(big.Int).Set(i)

	// Create a buffer for 32 characters
	chars := make([]byte, HashLength)

	// Extract 5 bits at a time from right to left (least significant first)
	// But we fill the string from right to left too, so it matches Big-Endian
	// i.e. last 5 bits of integer -> last char of string

	mask := big.NewInt(0x1f) // 5 ones

	for idx := HashLength - 1; idx >= 0; idx-- {
		// val = n & 0x1f
		val := new(big.Int).And(n, mask)
		chars[idx] = Alphabet[val.Int64()]

		// n = n >> 5
		n.Rsh(n, 5)
	}

	return string(chars)
}
