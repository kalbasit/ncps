package nar_test

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		url    string
		narURL nar.URL
		err    error
	}{
		{
			url: "",
			err: nar.ErrInvalidURL,
		},
		{
			url: "helloworld",
			err: nar.ErrInvalidURL,
		},
		{
			url: "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar",
			narURL: nar.URL{
				Hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values{},
			},
			err: nil,
		},
		{
			url: "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.bz2",
			narURL: nar.URL{
				Hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
				Compression: nar.CompressionTypeBzip2,
				Query:       url.Values{},
			},
			err: nil,
		},
		{
			url: "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.zst",
			narURL: nar.URL{
				Hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
				Compression: nar.CompressionTypeZstd,
				Query:       url.Values{},
			},
			err: nil,
		},
		{
			url: "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.lzip",
			narURL: nar.URL{
				Hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
				Compression: nar.CompressionTypeLzip,
				Query:       url.Values{},
			},
			err: nil,
		},
		{
			url: "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.lz4",
			narURL: nar.URL{
				Hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
				Compression: nar.CompressionTypeLz4,
				Query:       url.Values{},
			},
			err: nil,
		},
		{
			url: "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.br",
			narURL: nar.URL{
				Hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
				Compression: nar.CompressionTypeBr,
				Query:       url.Values{},
			},
			err: nil,
		},
		{
			url: "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.xz",
			narURL: nar.URL{
				Hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
				Compression: nar.CompressionTypeXz,
				Query:       url.Values{},
			},
			err: nil,
		},
		{
			url: "nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar?hash=1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
			narURL: nar.URL{
				Hash:        "1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values(map[string][]string{"hash": {"1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl"}}),
			},
			err: nil,
		},
		{
			url: "nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar.zst?hash=1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
			narURL: nar.URL{
				Hash:        "1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac",
				Compression: nar.CompressionTypeZstd,
				Query:       url.Values(map[string][]string{"hash": {"1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl"}}),
			},
			err: nil,
		},
		{
			url: "nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar.xz?hash=1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
			narURL: nar.URL{
				Hash:        "1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac",
				Compression: nar.CompressionTypeXz,
				Query:       url.Values(map[string][]string{"hash": {"1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl"}}),
			},
			err: nil,
		},
	}

	t.Parallel()

	for _, test := range tests {
		t.Run(fmt.Sprintf("ParseURL(%q)", test.url), func(t *testing.T) {
			t.Parallel()

			narURL, err := nar.ParseURL(test.url)

			if assert.ErrorIs(t, err, test.err) {
				assert.Equal(t, test.narURL, narURL)
			}
		})
	}
}

func TestParseUpstreamURL(t *testing.T) {
	t.Parallel()

	const fallback = "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps"

	t.Run("hash-named URL behaves exactly like ParseURL", func(t *testing.T) {
		t.Parallel()

		const u = "nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar.xz"

		got, err := nar.ParseUpstreamURL(u, fallback)
		require.NoError(t, err)

		want, err := nar.ParseURL(u)
		require.NoError(t, err)

		assert.Equal(t, want, got)
		assert.False(t, got.IsOpaque())
		assert.Empty(t, got.OpaquePath())
	})

	t.Run("opaque (UUID) URL preserves upstream path and keys off the fallback hash", func(t *testing.T) {
		t.Parallel()

		const u = "nar/d0c36585-67ac-4e1e-8747-3af0cbc09b90.nar.zst"

		got, err := nar.ParseUpstreamURL(u, fallback)
		require.NoError(t, err)

		// Storage key comes from the fallback (NarHash), not the URL.
		fp, err := got.ToFilePath()
		require.NoError(t, err)

		wantFP, err := nar.FilePath(fallback, nar.CompressionTypeZstd.ToFileExtension())
		require.NoError(t, err)
		assert.Equal(t, wantFP, fp)

		// Compression is still read from the URL.
		assert.Equal(t, nar.CompressionTypeZstd, got.Compression)

		// The opaque upstream path is preserved verbatim for the upstream GET.
		assert.True(t, got.IsOpaque())
		assert.Equal(t, "nar/d0c36585-67ac-4e1e-8747-3af0cbc09b90.nar.zst", got.OpaquePath())

		base, err := url.Parse("http://example.com")
		require.NoError(t, err)
		assert.Equal(
			t,
			"http://example.com/nar/d0c36585-67ac-4e1e-8747-3af0cbc09b90.nar.zst",
			got.JoinURL(base).String(),
		)
	})

	t.Run("opaque URL with an invalid fallback hash errors", func(t *testing.T) {
		t.Parallel()

		_, err := nar.ParseUpstreamURL("nar/d0c36585-67ac-4e1e-8747-3af0cbc09b90.nar.zst", "not-a-hash")
		assert.ErrorIs(t, err, nar.ErrInvalidHash)
	})

	t.Run("structurally invalid URL errors even with a valid fallback", func(t *testing.T) {
		t.Parallel()

		_, err := nar.ParseUpstreamURL("helloworld", fallback)
		assert.ErrorIs(t, err, nar.ErrInvalidURL)
	})
}

func TestWithOpaquePath(t *testing.T) {
	t.Parallel()

	base, err := url.Parse("http://example.com")
	require.NoError(t, err)

	const hash = "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps"

	u := nar.URL{Hash: hash, Compression: nar.CompressionTypeZstd}
	assert.False(t, u.IsOpaque())

	o := u.WithOpaquePath("nar/d0c36585-67ac-4e1e-8747-3af0cbc09b90.nar.zst")
	assert.True(t, o.IsOpaque())
	assert.Equal(
		t,
		"http://example.com/nar/d0c36585-67ac-4e1e-8747-3af0cbc09b90.nar.zst",
		o.JoinURL(base).String(),
	)

	// Storage key is unchanged by attaching an opaque path.
	fp, err := o.ToFilePath()
	require.NoError(t, err)
	wantFP, err := nar.FilePath(hash, nar.CompressionTypeZstd.ToFileExtension())
	require.NoError(t, err)
	assert.Equal(t, wantFP, fp)

	// The original URL is not mutated.
	assert.False(t, u.IsOpaque())
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input  nar.URL
		output nar.URL
		errStr string
	}{
		{
			input: nar.URL{
				Hash:        "09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values{},
			},
			output: nar.URL{
				Hash:        "09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values{},
			},
		},
		{
			input: nar.URL{
				Hash:        "c12lxpykv6sld7a0sakcnr3y0la70x8w-09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values{},
			},
			output: nar.URL{
				Hash:        "09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values{},
			},
		},
		{
			input: nar.URL{
				Hash:        "c12lxpykv6sld7a0sakcnr3y0la70x8w_09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0",
				Compression: nar.CompressionTypeZstd,
				Query:       url.Values(map[string][]string{"hash": {"123"}}),
			},
			output: nar.URL{
				Hash:        "09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0",
				Compression: nar.CompressionTypeZstd,
				Query:       url.Values(map[string][]string{"hash": {"123"}}),
			},
		},
		{
			// Valid hash with separator but no prefix
			input: nar.URL{
				Hash: "1m9phnql68mxrnjc7ssxcvjrxxwcx0fzc849w025mkanwgsy1bpy",
			},
			output: nar.URL{
				Hash: "1m9phnql68mxrnjc7ssxcvjrxxwcx0fzc849w025mkanwgsy1bpy",
			},
		},
		{
			// Valid prefix and multiple separators in the suffix
			input: nar.URL{
				Hash: "c12lxpykv6sld7a0sakcnr3y0la70x8w-1m9phnql68mxrnjc7ssxcvjrxxwcx0fzc849w025mkanwgsy1bpy",
			},
			output: nar.URL{
				Hash: "1m9phnql68mxrnjc7ssxcvjrxxwcx0fzc849w025mkanwgsy1bpy",
			},
		},
		{
			// Potential path traversal attempt in the hash (should remain unchanged or be sanitized)
			input: nar.URL{
				Hash: "c12lxpykv6sld7a0sakcnr3y0la70x8w-../../etc/passwd",
			},
			errStr: "invalid nar hash: ../../etc/passwd",
		},
	}

	for _, test := range tests {
		tname := fmt.Sprintf(
			"Normalize(%q) -> %q",
			test.input.Hash,
			test.output.Hash,
		)
		t.Run(tname, func(t *testing.T) {
			t.Parallel()

			result, err := test.input.Normalize()
			if test.errStr != "" {
				assert.EqualError(t, err, test.errStr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, test.output, result)
			}
		})
	}
}

func TestJoinURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		narURL nar.URL

		url string
	}{
		{
			narURL: nar.URL{
				Hash:        "abc123",
				Compression: nar.CompressionTypeNone,
			},
			url: "http://example.com/nar/abc123.nar",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeBzip2,
			},
			url: "http://example.com/nar/def456.nar.bz2",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeZstd,
			},
			url: "http://example.com/nar/def456.nar.zst",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeXz,
			},
			url: "http://example.com/nar/def456.nar.xz",
		},
		{
			narURL: nar.URL{
				Hash:        "abc123",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values(map[string][]string{"hash": {"123"}}),
			},
			url: "http://example.com/nar/abc123.nar?hash=123",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeZstd,
				Query:       url.Values(map[string][]string{"hash": {"123"}}),
			},
			url: "http://example.com/nar/def456.nar.zst?hash=123",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeXz,
				Query:       url.Values(map[string][]string{"hash": {"123"}}),
			},
			url: "http://example.com/nar/def456.nar.xz?hash=123",
		},
	}

	for _, test := range tests {
		tname := fmt.Sprintf(
			"URL(%q, %q, %q).ToFilePath() -> %q",
			test.narURL.Hash,
			test.narURL.Compression,
			test.narURL.Query.Encode(),
			test.url,
		)

		t.Run(tname, func(t *testing.T) {
			t.Parallel()

			u, err := url.Parse("http://example.com")
			require.NoError(t, err)

			assert.Equal(t, test.url, test.narURL.JoinURL(u).String())
		})
	}
}

func TestString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		narURL nar.URL

		string string
	}{
		{
			narURL: nar.URL{
				Hash: "abc123",
			},
			string: "nar/abc123.nar",
		},
		{
			narURL: nar.URL{
				Hash:        "abc123",
				Compression: nar.CompressionTypeNone,
			},
			string: "nar/abc123.nar",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeBzip2,
			},
			string: "nar/def456.nar.bz2",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeZstd,
			},
			string: "nar/def456.nar.zst",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeXz,
			},
			string: "nar/def456.nar.xz",
		},
		{
			narURL: nar.URL{
				Hash:        "abc123",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values(map[string][]string{"hash": {"123"}}),
			},
			string: "nar/abc123.nar?hash=123",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeZstd,
				Query:       url.Values(map[string][]string{"hash": {"123"}}),
			},
			string: "nar/def456.nar.zst?hash=123",
		},
		{
			narURL: nar.URL{
				Hash:        "def456",
				Compression: nar.CompressionTypeXz,
				Query:       url.Values(map[string][]string{"hash": {"123"}}),
			},
			string: "nar/def456.nar.xz?hash=123",
		},
	}

	for _, test := range tests {
		tname := fmt.Sprintf(
			"URL(%q, %q, %q).String() -> %q",
			test.narURL.Hash,
			test.narURL.Compression,
			test.narURL.Query.Encode(),
			test.string,
		)
		t.Run(tname, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.string, test.narURL.String())
		})
	}
}
