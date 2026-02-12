package testhelper

import "io"

const (
	// AllChars refers to unexported chars constant.
	AllChars = allChars

	// Nix32Chars refers to unexported nix32Chars constant.
	Nix32Chars = nix32Chars
)

// RandChars is a test-only export of the unexported randChars method.
func RandChars(n int, charSet string, r io.Reader) (string, error) {
	return randChars(n, charSet, r)
}
