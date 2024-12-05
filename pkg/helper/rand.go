package helper

import (
	"crypto/rand"
	"io"
	"math/big"
)

const chars = "abcdefghijklmnopqrstuvwxyz0123456789"

// RandString returns a random string of length n using r as the
// random reader; If r is nil, rand.Reader will be used instead.
func RandString(n int, r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}

	ret := make([]byte, n)

	for i := 0; i < n; i++ {
		num, err := rand.Int(r, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}

		ret[i] = chars[num.Int64()]
	}

	return string(ret), nil
}
