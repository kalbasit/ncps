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

	narRegexp = regexp.MustCompile(`^nar/([a-z0-9]+)\.nar(?:\.([a-z0-9]+))?$`)
)

// URL represents a nar URL.
type URL struct {
	Hash        string
	Compression string
}

// ParseURL parses a nar URL (as present in narinfo) and returns its components.
func ParseURL(u string) (URL, error) {
	var nu URL

	if u == "" || !strings.HasPrefix(u, "nar/") {
		return nu, ErrInvalidURL
	}

	sm := narRegexp.FindStringSubmatch(u)
	if len(sm) != 3 {
		return nu, ErrInvalidURL
	}

	nu.Hash = sm[1]
	nu.Compression = sm[2]

	return nu, nil
}

// NewLogger returns a new logger with the right fields.
func (u URL) NewLogger(log log15.Logger) log15.Logger {
	return log.New(
		"hash", u.Hash,
		"compression", u.Compression,
	)
}

// ToFilePath returns the filepath in the store for a given nar URL.
func (u URL) ToFilePath() string {
	// TODO: bring it out of the helper
	return helper.NarFilePath(u.Hash, u.Compression)
}

// JoinURL returns a new URL combined with the given URL.
func (u URL) JoinURL(uri *url.URL) *url.URL {
	p := "/nar/" + u.Hash + ".nar"

	if u.Compression != "" {
		p += "." + u.Compression
	}

	uri = uri.JoinPath(p)

	return uri
}
