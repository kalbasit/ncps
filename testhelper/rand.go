package testhelper

import (
	"crypto/rand"
	"io"
	"math/big"
)

const (
	allChars    = "abcdefghijklmnopqrstuvwxyz0123456789"
	nix32Chars  = "abcdfghijklmnpqrsvwxyz0123456789"
	base16Chars = "0123456789abcdef"
)

func randChars(n int, charSet string, r io.Reader) (string, error) {
	ret := make([]byte, n)

	for i := range n {
		num, err := rand.Int(r, big.NewInt(int64(len(charSet))))
		if err != nil {
			return "", err
		}

		ret[i] = charSet[num.Int64()]
	}

	return string(ret), nil
}

// RandString returns a random string of length n using crypto/rand.Reader as
// the random reader.
func RandString(n int) (string, error) { return randChars(n, allChars, rand.Reader) }

// MustRandString returns the string returned by RandString. If RandString
// returns an error, it will panic.
func MustRandString(n int) string {
	str, err := RandString(n)
	if err != nil {
		panic(err)
	}

	return str
}

func RandNarInfoHash() (string, error) {
	return randChars(32, nix32Chars, rand.Reader)
}

func MustRandNarInfoHash() string {
	str, err := RandNarInfoHash()
	if err != nil {
		panic(err)
	}

	return str
}

func RandBase32NarHash() (string, error) {
	return randChars(52, nix32Chars, rand.Reader)
}

func MustRandBase32NarHash() string {
	str, err := RandBase32NarHash()
	if err != nil {
		panic(err)
	}

	return str
}

func RandBase16NarHash() (string, error) {
	return randChars(64, base16Chars, rand.Reader)
}

func MustRandBase16NarHash() string {
	str, err := RandBase16NarHash()
	if err != nil {
		panic(err)
	}

	return str
}
