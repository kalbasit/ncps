package nar_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

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
				Compression: "",
			},
			err: nil,
		},
		{
			url: "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.xz",
			narURL: nar.URL{
				Hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
				Compression: "xz",
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

func TestNarURLPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hash        string
		compression string
		path        string
	}{
		{hash: "", compression: "", path: "/nar/.nar"}, // not really valid but it is what it is
		{hash: "abc123", compression: "", path: "/nar/abc123.nar"},
		{hash: "def456", compression: "xz", path: "/nar/def456.nar.xz"},
	}

	for _, test := range tests {
		tname := fmt.Sprintf("URL(%q, %q).ToFilePath() -> %q", test.hash, test.compression, test.path)

		t.Run(tname, func(t *testing.T) {
			t.Parallel()

			nu := nar.URL{
				Hash:        test.hash,
				Compression: test.compression,
			}

			assert.Equal(t, test.path, nu.ToNetURLPath())
		})
	}
}
