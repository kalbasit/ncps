package nar

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rs/zerolog"
)

var (
	// ErrInvalidURL is returned if the regexp did not match the given URL.
	ErrInvalidURL = errors.New("invalid nar URL")

	// hashValidationRegexp validates that a string matches the HashPattern.
	hashValidationRegexp = regexp.MustCompile(`^(` + HashPattern + `)$`)
)

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
	if !hashValidationRegexp.MatchString(hash) {
		return URL{}, ErrInvalidURL
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
func (u URL) Normalize() URL {
	hash := u.Hash

	// Find the first separator ('-' or '_').
	idx := strings.IndexAny(hash, "-_")

	// If a separator is found after the first character.
	if idx > 0 {
		prefix := hash[:idx]
		suffix := hash[idx+1:]

		// A narinfo hash prefix is typically 32 characters long. This is a strong signal.
		// We check this and ensure the suffix is not empty.
		if len(prefix) == 32 && len(suffix) > 0 {
			hash = suffix
		}
	}

	// Sanitize the hash to prevent path traversal.
	// Even though ParseURL validates the hash, URL is a public struct
	// and Normalize could be called on a manually constructed URL.
	cleanedHash := filepath.Clean(hash)
	if strings.Contains(cleanedHash, "..") || strings.HasPrefix(cleanedHash, "/") {
		// If the cleaned hash is still invalid, we return the original URL
		// to avoid potentially breaking something that might be valid in some context,
		// but storage layers will still validate it using ToFilePath().
		return u
	}

	return URL{
		Hash:        cleanedHash,
		Compression: u.Compression,
		Query:       u.Query,
	}
}
