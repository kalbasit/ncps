//nolint:testpackage
package nixcacheinfo

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitOnce(t *testing.T) {
	tests := []struct {
		s    string
		sep  string
		str1 string
		str2 string
		err  string
	}{
		{"hello:world", ":", "hello", "world", ""},
		{":helloworld", ":", "", "helloworld", ""},
		{"helloworld:", ":", "helloworld", "", ""},
		{"helloworld", ":", "", "", "found no separators in the string: separator=\":\" string=\"helloworld\""},
		{"hello:wo:rld", ":", "", "", "found multiple separators in the string: separator=\":\" string=\"hello:wo:rld\""},
		{"hello::world", ":", "", "", "found multiple separators in the string: separator=\":\" string=\"hello::world\""},
	}

	t.Parallel()

	for _, test := range tests {
		tName := fmt.Sprintf("splitOnce(%q, %q) -> (%q, %q, %s)",
			test.s, test.sep, test.str1, test.str2, test.err)

		t.Run(tName, func(t *testing.T) {
			t.Parallel()

			str1, str2, err := splitOnce(test.s, test.sep)

			if test.err == "" && err != nil {
				t.Fatalf("expected no error but got %s", err)
			} else if test.err != "" && err == nil {
				t.Fatalf("expected an error but got none")
			} else if test.err != "" && err != nil {
				assert.EqualError(t, err, test.err)
			}

			assert.Equal(t, test.str1, str1)
			assert.Equal(t, test.str2, str2)
		})
	}
}
