//nolint:testpackage
package helper

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseNarURL(t *testing.T) {
	tests := []struct {
		url         string
		hash        string
		compression string
		err         error
	}{
		{
			url: "",
			err: ErrInvalidNarURL,
		},
		{
			url: "helloworld",
			err: ErrInvalidNarURL,
		},
		{
			url:         "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar",
			hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
			compression: "",
			err:         nil,
		},
		{
			url:         "nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar.xz",
			hash:        "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps",
			compression: "xz",
			err:         nil,
		},
		{
			url:         "nar/1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac.nar?hash=1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
			hash:        "1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac",
			compression: "",
			err:         nil,
		},
	}

	t.Parallel()

	for _, test := range tests {
		t.Run(fmt.Sprintf("ParseNarURL(%q) -> (%q, %q, %s)",
			test.url, test.hash, test.compression, test.err), func(t *testing.T) {
			t.Parallel()

			hash, compression, err := ParseNarURL(test.url)

			assert.Equal(t, test.hash, hash)
			assert.Equal(t, test.compression, compression)
			assert.ErrorIs(t, test.err, err)
		})
	}
}
