package nar_test

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
)

func FuzzParseURL(f *testing.F) {
	tests := []string{
		"",
		"helloworld",
		"nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar",
		"nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.bz2",
		"nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.zst",
		"nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.lzip",
		"nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.lz4",
		"nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.br",
		"nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.xz",
		"nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar?hash=1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
		"nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar.zst?hash=1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
		"nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar.xz?hash=1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
	}

	for _, tc := range tests {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, url string) {
		narURL, err := nar.ParseURL(url)
		if err != nil {
			assert.Equal(t, "", narURL.Hash)
			assert.Equal(t, "", string(narURL.Compression))
			assert.Equal(t, "", narURL.Query.Encode())
		} else {
			assert.NotEmpty(t, narURL.Hash)
			assert.NotEmpty(t, string(narURL.Compression))
		}
	})
}

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

			if assert.ErrorIs(t, test.err, err) {
				assert.Equal(t, test.narURL, narURL)
			}
		})
	}
}

func FuzzJoinURL(f *testing.F) {
	hashes := []string{
		"1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
		"1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
		"1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
		"1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
		"1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
		"1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
		"1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
		"1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac",
		"1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac",
		"1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac",
	}

	queries := []string{
		"",
		"a=1",
		"a=1&b=2",
	}

	urls := []string{
		"http://example.com",
		"example.com",
	}

	for _, uri := range urls {
		for _, hash := range hashes {
			for _, query := range queries {
				f.Add(uri, hash, query)
			}
		}
	}

	f.Fuzz(func(t *testing.T, uri, hash, query string) {
		q, err := url.ParseQuery(query)
		if err != nil {
			t.Skip()
		}

		u1, err := url.Parse(uri)
		if err != nil {
			t.Skip()
		}

		narURL := nar.URL{
			Hash:        hash,
			Compression: "xz",
			Query:       q,
		}

		u2 := narURL.JoinURL(u1)

		assert.Equal(t, u1.Scheme, u2.Scheme)
		assert.Equal(t, u1.Host, u2.Host)
		assert.Equal(t, u1.JoinPath("/nar/"+hash+".nar.xz").Path, u2.Path)
	})
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
