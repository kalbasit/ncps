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
		// Recover ".nar"-less opaque upstream URLs (e.g. snix-castore's
		// "nar/snix-castore/<blob>?narsize=N"). The Nix binary-cache protocol
		// treats the narinfo URL as an opaque path relative to the cache root, so
		// a pull-through proxy must tolerate paths without a ".nar" token rather
		// than reject them. Such URLs carry no compression extension and are
		// served uncompressed, so compression is none; the storage key comes from
		// the narinfo NarHash. Only genuine cache-relative paths are recovered —
		// not bare tokens like "helloworld".
		if op, oq, ok := parseOpaqueNoNarURL(u); errors.Is(err, ErrInvalidURL) && ok {
			if verr := ValidateHash(fallbackHash); verr != nil {
				return URL{}, fmt.Errorf("opaque nar URL %q needs a valid fallback hash: %w", u, verr)
			}

			return URL{
				Hash:        fallbackHash,
				Compression: CompressionTypeNone,
				Query:       oq,
				opaquePath:  op,
			}, nil
		}

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

// parseOpaqueNoNarURL recognises an opaque upstream NAR URL that has no ".nar"
// token at all (e.g. snix-castore's "nar/snix-castore/<blob>?narsize=N"). It
// returns the path (query stripped), the parsed query, and ok=true only for a
// genuine cache-relative path: the path must contain a "/" separator (a bare
// token such as "helloworld" is not a NAR URL) and its filename must not itself
// contain ".nar" (that is a malformed conventional URL, handled by parseURLParts,
// not an opaque one). The caller supplies compression (none) and the storage key.
func parseOpaqueNoNarURL(u string) (pathPart string, query url.Values, ok bool) {
	pathPart, rawQuery, _ := strings.Cut(u, "?")
	if pathPart == "" || !strings.Contains(pathPart, "/") {
		return "", nil, false
	}

	filename := filepath.Base(pathPart)
	if filename == "" || filename == "." || strings.Contains(filename, ".nar") {
		return "", nil, false
	}

	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", nil, false
	}

	return pathPart, query, true
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

// OpaqueUpstreamRef returns the opaque upstream path together with its query
// string (e.g. snix-castore's "nar/snix-castore/<blob>?narsize=N"), the form
// that must be persisted so the upstream GET can be reconstructed verbatim after
// local eviction. It returns "" for a conventional hash-named URL. Opaque URLs
// that carry no query (e.g. cachix UUID NARs) yield just the path, so the
// persisted representation is unchanged for them.
func (u URL) OpaqueUpstreamRef() string {
	if u.opaquePath == "" {
		return ""
	}

	if q := u.Query.Encode(); q != "" {
		return u.opaquePath + "?" + q
	}

	return u.opaquePath
}

// WithOpaqueUpstreamRef returns a copy of the URL with an opaque upstream
// reference (as produced by OpaqueUpstreamRef) restored: the path becomes the
// opaque path used for the upstream GET and any query string is parsed back onto
// the URL so it survives onto the reconstructed request. A reference without a
// query behaves exactly like WithOpaquePath.
func (u URL) WithOpaqueUpstreamRef(ref string) URL {
	path, rawQuery, hasQuery := strings.Cut(ref, "?")
	u.opaquePath = path

	if hasQuery {
		if q, err := url.ParseQuery(rawQuery); err == nil {
			u.Query = q
		}
	}

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
