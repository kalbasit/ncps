package nar_test

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/stretchr/testify/assert"
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
				Query:       url.Values{},
			},
			err: nil,
		},
		{
			url: "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.xz",
			narURL: nar.URL{
				Hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
				Compression: "xz",
				Query:       url.Values{},
			},
			err: nil,
		},
		{
			url: "nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar?hash=1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
			narURL: nar.URL{
				Hash:        "1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac",
				Compression: "",
				Query:       url.Values(map[string][]string{"hash": []string{"1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl"}}),
			},
			err: nil,
		},
		{
			url: "nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar.xz?hash=1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
			narURL: nar.URL{
				Hash:        "1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac",
				Compression: "xz",
				Query:       url.Values(map[string][]string{"hash": []string{"1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl"}}),
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
