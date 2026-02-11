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

// Normalize returns a new URL with the narinfo hash prefix trimmed from the Hash.
// nix-serve serves NAR URLs with the narinfo hash as a prefix (e.g., "narinfo-hash-actual-hash").
// This method removes that prefix to standardize the hash for storage.
func (u URL) Normalize() URL {
	// The prefix is typically a hash followed by a dash/underscore, then the actual NAR hash.
	// We need to find the last occurrence of a dash or underscore and trim everything before it.
	hash := u.Hash

	// Look for the pattern: narinfo_hash-(or_)actual_nar_hash
	// We identify this by looking for the last dash or underscore that separates two hash-like parts
	parts := strings.FieldsFunc(hash, func(r rune) bool {
		return r == '-' || r == '_'
	})

	// If we have more than one part, check if the first part looks like a narinfo hash
	// and the remaining parts form the actual NAR hash
	if len(parts) > 1 {
		// A narinfo hash is typically 32 characters, and a NAR hash is also typically 52+ characters
		// We use a heuristic: if the first part is shorter than the second part,
		// it's likely the narinfo prefix
		if len(parts[0]) < len(parts[1]) {
			// Remove the first part and the separator
			// Find the position of the first separator
			firstSepIdx := strings.IndexAny(hash, "-_")
			if firstSepIdx > 0 {
				hash = hash[firstSepIdx+1:]
			}
		}
	}

	return URL{
		Hash:        hash,
		Compression: u.Compression,
		Query:       u.Query,
	}
}
