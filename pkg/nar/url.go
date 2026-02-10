package nar

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/rs/zerolog"
)

var (
	// ErrInvalidURL is returned if the regexp did not match the given URL.
	ErrInvalidURL = errors.New("invalid nar URL")

	// https://regex101.com/r/yPwxpw/4
	narRegexp = regexp.MustCompile(`^nar/(` + narHashPattern + `)\.nar(\.([a-z0-9]+))?(\?([a-z0-9=&]*))?$`)
)

// URL represents a nar URL.
type URL struct {
	Hash        string
	Compression CompressionType
	Query       url.Values
}

// ParseURL parses a nar URL (as present in narinfo) and returns its components.
func ParseURL(u string) (URL, error) {
	if u == "" || !strings.HasPrefix(u, "nar/") {
		return URL{}, ErrInvalidURL
	}

	sm := narRegexp.FindStringSubmatch(u)
	if len(sm) != 6 {
		return URL{}, ErrInvalidURL
	}

	nu := URL{Hash: sm[1]}

	var err error

	if nu.Compression, err = CompressionTypeFromExtension(sm[3]); err != nil {
		return URL{}, fmt.Errorf("error computing the compression type: %w", err)
	}

	if nu.Query, err = url.ParseQuery(sm[5]); err != nil {
		return URL{}, fmt.Errorf("error parsing the RawQuery as url.Values: %w", err)
	}

	return nu, nil
}

// NewLogger returns a new logger with the right fields.
func (u URL) NewLogger(log zerolog.Logger) zerolog.Logger {
	return log.With().
		Str("nar_hash", u.Hash).
		Str("nar_compression", u.Compression.String()).
		Str("nar_query", u.Query.Encode()).
		Logger()
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
func (u URL) ToFilePath() (string, error) {
	return FilePath(u.Hash, u.Compression.ToFileExtension())
}

func (u URL) pathWithCompression() string {
	p := "nar/" + u.Hash + ".nar"

	if e := u.Compression.ToFileExtension(); e != "" {
		p += "." + e
	}

	return p
}
