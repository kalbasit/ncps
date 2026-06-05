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
	Hash            string
	Compression     CompressionType
	Query           url.Values
	TransparentZstd bool

	// opaquePath holds the original upstream path (e.g. "nar/<uuid>.nar.zst")
	// when the narinfo URL is not hash-named and therefore cannot be
	// reconstructed from Hash. It is used exclusively for the upstream GET;
	// the Hash field still drives ncps's local storage key. It is empty for
	// conventional hash-named URLs. See ParseUpstreamURL.
	opaquePath string
}

// ParseURL parses a nar URL (as present in narinfo) and returns its components.
// It accepts URLs in the format: [path/]<hash>.nar[.<compression>][?query]
// The hash must match HashPattern. This implementation is flexible about the
// directory structure - only the filename matters, not the "nar/" prefix.
func ParseURL(u string) (URL, error) {
	_, hash, ct, query, err := parseURLParts(u)
	if err != nil {
		return URL{}, err
	}

	// Validate that the hash matches HashPattern.
	if err := ValidateHash(hash); err != nil {
		return URL{}, err
	}

	return URL{
		Hash:        hash,
		Compression: ct,
		Query:       query,
	}, nil
}

// ParseUpstreamURL parses a nar URL taken from an upstream narinfo. For
// conventional hash-named URLs it is identical to ParseURL. For opaque URLs —
// where the filename before ".nar" is not a valid Nix hash, as served by
// cachix (e.g. "nar/<uuidv4>.nar.zst") — it preserves the original path for the
// upstream GET and uses fallbackHash (the narinfo's NarHash) as ncps's internal
// storage key. fallbackHash must be a valid hash; otherwise ErrInvalidHash is
// returned.
//
// The Nix binary-cache protocol treats the narinfo URL field as an opaque path
// relative to the cache root, so a pull-through proxy must tolerate non
// hash-named URLs rather than reject them.
func ParseUpstreamURL(u, fallbackHash string) (URL, error) {
	pathPart, hash, ct, query, err := parseURLParts(u)
	if err != nil {
		return URL{}, err
	}

	// Fast path: a conventional hash-named URL behaves exactly like ParseURL.
	if ValidateHash(hash) == nil {
		return URL{
			Hash:        hash,
			Compression: ct,
			Query:       query,
		}, nil
	}

	// Opaque URL: the storage key must come from the narinfo's NarHash.
	if err := ValidateHash(fallbackHash); err != nil {
		return URL{}, fmt.Errorf("opaque nar URL %q needs a valid fallback hash: %w", u, err)
	}

	return URL{
		Hash:        fallbackHash,
		Compression: ct,
		Query:       query,
		opaquePath:  pathPart,
	}, nil
}

// parseURLParts splits a nar URL into its path, hash, compression and query
// components without validating the hash. ParseURL and ParseUpstreamURL share
// this and apply their own hash policy.
func parseURLParts(u string) (pathPart, hash string, ct CompressionType, query url.Values, err error) {
	if u == "" {
		return "", "", "", nil, ErrInvalidURL
	}

	// Separate the query string from the path
	pathPart, rawQuery, _ := strings.Cut(u, "?")

	// Get the filename (last component of the path)
	filename := filepath.Base(pathPart)
	if filename == "" || filename == "." {
		return "", "", "", nil, ErrInvalidURL
	}

	// The filename must contain ".nar" followed by optional compression extension
	// Format: hash.nar[.compression]
	// Everything before .nar is the hash, everything after is optional compression
	hash, afterNar, found := strings.Cut(filename, ".nar")
	if !found || hash == "" {
		return "", "", "", nil, ErrInvalidURL
	}

	// Extract compression extension (e.g., ".bz2" -> "bz2", "" -> "")
	var compression string

	if afterNar != "" {
		// afterNar should start with a dot
		if !strings.HasPrefix(afterNar, ".") {
			return "", "", "", nil, ErrInvalidURL
		}

		compression = afterNar[1:] // remove leading dot
	}

	// Determine compression type
	ct, err = CompressionTypeFromExtension(compression)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("error computing the compression type: %w", err)
	}

	// Parse the query string if present
	query, err = url.ParseQuery(rawQuery)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("error parsing the RawQuery as url.Values: %w", err)
	}

	return pathPart, hash, ct, query, nil
}

// IsOpaque reports whether the URL carries a preserved opaque upstream path
// (i.e. the narinfo URL was not hash-named and the storage key was derived
// from the narinfo NarHash instead).
func (u URL) IsOpaque() bool { return u.opaquePath != "" }

// OpaquePath returns the preserved opaque upstream path, or "" for a
// conventional hash-named URL. Persist this so the NAR can be re-fetched from
// upstream after the local copy is evicted.
func (u URL) OpaquePath() string { return u.opaquePath }

// WithOpaquePath returns a copy of the URL with the given opaque upstream path
// attached, used when restoring a persisted opaque path so the upstream GET
// targets the original path while local storage stays keyed off Hash.
func (u URL) WithOpaquePath(path string) URL {
	u.opaquePath = path

	return u
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
	// Opaque upstream URLs cannot be reconstructed from Hash; the original
	// path was preserved verbatim and is used for the upstream GET.
	if u.opaquePath != "" {
		return u.opaquePath
	}

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
