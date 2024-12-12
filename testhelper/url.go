package testhelper

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// MustParseURL parses the url (string) and returns or fails the test.
func MustParseURL(t *testing.T, us string) *url.URL {
	t.Helper()

	u, err := url.Parse(us)
	require.NoError(t, err)

	return u
}
