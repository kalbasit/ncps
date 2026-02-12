package nar

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
)

// ErrInvalidURL is returned if the regexp did not match the given URL.
var ErrInvalidURL = errors.New("invalid nar URL")

// URL represents a nar URL.
type URL struct {
	Hash        string
	Compression CompressionType
	Query       url.Values
}

// ParseURL parses a nar URL (as present in narinfo) and returns its components.
// It accepts URLs in the format: [path/]<hash>.nar[.<compression>][?query]
// The hash must match HashPattern. This implementation is flexible about the
// directory structure - only the filename matters, not the "nar/" prefix.
func ParseURL(u string) (URL, error) {
	if u == "" {
		return URL{}, ErrInvalidURL
	}

	// Separate the query string from the path
	pathPart, rawQuery, _ := strings.Cut(u, "?")

	// Get the filename (last component of the path)
	filename := filepath.Base(pathPart)
	if filename == "" || filename == "." {
		return URL{}, ErrInvalidURL
	}

	// The filename must contain ".nar" followed by optional compression extension
	// Format: hash.nar[.compression]
	// Everything before .nar is the hash, everything after is optional compression
	hash, afterNar, found := strings.Cut(filename, ".nar")
	if !found || hash == "" {
		return URL{}, ErrInvalidURL
	}

	// Validate that the hash matches HashPattern before processing further
	if err := ValidateHash(hash); err != nil {
		return URL{}, err
	}

	// Extract compression extension (e.g., ".bz2" -> "bz2", "" -> "")
	var compression string

	if afterNar != "" {
		// afterNar should start with a dot
		if !strings.HasPrefix(afterNar, ".") {
			return URL{}, ErrInvalidURL
		}

		compression = afterNar[1:] // remove leading dot
	}

	// Determine compression type
	ct, err := CompressionTypeFromExtension(compression)
	if err != nil {
		return URL{}, fmt.Errorf("error computing the compression type: %w", err)
	}

	// Parse the query string if present
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return URL{}, fmt.Errorf("error parsing the RawQuery as url.Values: %w", err)
	}

	return URL{
		Hash:        hash,
		Compression: ct,
		Query:       query,
	}, nil
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
func (u URL) Normalize() (URL, error) {
	hash := u.Hash

	// First, try the lenient regex to extract prefix and potential hash part
	sm := narHashLenientRegexp.FindStringSubmatch(hash)
	if len(sm) < 3 {
		return URL{}, fmt.Errorf("%w: %s", ErrInvalidHash, hash)
	}

	// sm[0] is the entire match
	// sm[1] is the optional prefix with separator (e.g., "abc-" or "abc_"), or empty if no prefix
	// sm[2] is everything after the optional prefix (always the actual hash we want)
	actualHash := sm[2]

	// Strictly validate the extracted hash matches the normalized pattern
	if !narNormalizedHashRegexp.MatchString(actualHash) {
		return URL{}, fmt.Errorf("%w: %s", ErrInvalidHash, actualHash)
	}

	return URL{
		Hash:        actualHash,
		Compression: u.Compression,
		Query:       u.Query,
	}, nil
}
