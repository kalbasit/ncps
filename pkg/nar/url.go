package nar

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/inconshreveable/log15/v3"
)

var (
	// ErrInvalidURL is returned if the regexp did not match the given URL.
	ErrInvalidURL = errors.New("invalid nar URL")

	// https://regex101.com/r/yPwxpw/2
	narRegexp = regexp.MustCompile(`^nar/([a-z0-9]+)\.nar(\.([a-z0-9]+))?(\?([a-z0-9=&]*))?$`)
)

// URL represents a nar URL.
type URL struct {
	Hash        string
	Compression string
	Query       url.Values
}

// ParseURL parses a nar URL (as present in narinfo) and returns its components.
func ParseURL(u string) (URL, error) {
	var nu URL

	if u == "" || !strings.HasPrefix(u, "nar/") {
		return nu, ErrInvalidURL
	}

	sm := narRegexp.FindStringSubmatch(u)
	if len(sm) != 6 {
		return nu, ErrInvalidURL
	}

	nu.Hash = sm[1]
	nu.Compression = sm[3]

	var err error
	if nu.Query, err = url.ParseQuery(sm[5]); err != nil {
		return nu, err
	}

	return nu, nil
}

// NewLogger returns a new logger with the right fields.
func (u URL) NewLogger(log log15.Logger) log15.Logger {
	return log.New(
		"hash", u.Hash,
		"compression", u.Compression,
		"query", u.Query.Encode(),
	)
}
