package nar

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/inconshreveable/log15/v3"

	"github.com/kalbasit/ncps/pkg/helper"
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

// JoinURL returns a new URL combined with the given URL.
func (u URL) JoinURL(uri *url.URL) *url.URL {
	uri = uri.JoinPath("/" + u.pathWithCompression())

	if q := u.Query.Encode(); q != "" {
		if uri.RawQuery != "" {
			uri.RawQuery += "&"
		}

		uri.RawQuery += q
	}

	return uri
}

// String returns the URL as a string.
func (u URL) String() string {
	p := u.pathWithCompression()

	if q := u.Query.Encode(); q != "" {
		p += "?" + q
	}

	return p
}

// ToFilePath returns the filepath in the store for a given nar URL.
func (u URL) ToFilePath() string {
	// TODO: bring it out of the helper
	return helper.NarFilePath(u.Hash, u.Compression)
}

func (u URL) pathWithCompression() string {
	p := "nar/" + u.Hash + ".nar"

	if u.Compression != "" {
		p += "." + u.Compression
	}

	return p
}
