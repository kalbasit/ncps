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
		{
			url: "nar/mxz31j8mf7kddi3vipqhxhdm4lswiyl0-0hgyymhfxkkr8xqfciql7ygapy04m2m8finhbbnx8c44frp6v77c.nar",
			narURL: nar.URL{
				Hash:        "mxz31j8mf7kddi3vipqhxhdm4lswiyl0-0hgyymhfxkkr8xqfciql7ygapy04m2m8finhbbnx8c44frp6v77c",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values{},
			},
		},
		{
			url: "nar/mxz31j8mf7kddi3vipqhxhdm4lswiyl0_0hgyymhfxkkr8xqfciql7ygapy04m2m8finhbbnx8c44frp6v77c.nar",
			narURL: nar.URL{
				Hash:        "mxz31j8mf7kddi3vipqhxhdm4lswiyl0_0hgyymhfxkkr8xqfciql7ygapy04m2m8finhbbnx8c44frp6v77c",
				Compression: nar.CompressionTypeNone,
				Query:       url.Values{},
			},
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

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input  nar.URL
		output nar.URL
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
	}

	for _, test := range tests {
		tname := fmt.Sprintf(
			"Normalize(%q) -> %q",
			test.input.Hash,
			test.output.Hash,
		)
		t.Run(tname, func(t *testing.T) {
			t.Parallel()

			result := test.input.Normalize()
			assert.Equal(t, test.output, result)
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
