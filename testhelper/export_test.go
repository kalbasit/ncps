package testhelper

import "io"

// AllChars refers to unexported chars constant.
const AllChars = allChars

// RandChars is a test-only export of the unexported randChars method.
func RandChars(n int, charSet string, r io.Reader) (string, error) {
	return randChars(n, charSet, r)
}
