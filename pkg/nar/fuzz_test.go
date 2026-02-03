package nar_test

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"

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
			assert.Empty(t, narURL.Hash)
			assert.Empty(t, string(narURL.Compression))
			assert.Empty(t, narURL.Query.Encode())
		} else {
			assert.NotEmpty(t, narURL.Hash)
			assert.NotEmpty(t, string(narURL.Compression))
		}
	})
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
