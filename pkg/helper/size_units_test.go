package helper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/helper"
)

func TestParseSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		sizeStr string
		size    uint64
		err     string
	}{
		// uppercase
		{sizeStr: "2B", size: 2, err: ""},
		{sizeStr: "3K", size: 3072, err: ""},
		{sizeStr: "4M", size: 4194304, err: ""},
		{sizeStr: "9G", size: 9663676416, err: ""},
		{sizeStr: "10T", size: 10995116277760, err: ""},

		// lowercase
		{sizeStr: "2b", size: 2, err: ""},
		{sizeStr: "3k", size: 3072, err: ""},
		{sizeStr: "4m", size: 4194304, err: ""},
		{sizeStr: "9g", size: 9663676416, err: ""},
		{sizeStr: "10t", size: 10995116277760, err: ""},

		// errors
		{sizeStr: "20", err: "error parsing the unit for \"20\": invalid size suffix"},
		{sizeStr: "2a", err: "error parsing the unit for \"2a\": invalid size suffix"},
		{sizeStr: "2A", err: "error parsing the unit for \"2A\": invalid size suffix"},
		{sizeStr: "2Gb", err: "strconv.ParseUint: parsing \"2G\": invalid syntax"},
	}

	for _, test := range tests {
		tn := fmt.Sprintf("ParseSize(%q) -> (%d, %q)", test.sizeStr, test.size, test.err)
		t.Run(tn, func(t *testing.T) {
			t.Parallel()

			s, err := helper.ParseSize(test.sizeStr)
			assert.Equal(t, test.size, s)

			if test.err == "" {
				assert.Nil(t, err)
			} else {
				if assert.NotNil(t, err) {
					assert.Equal(t, test.err, err.Error())
				}
			}
		})
	}
}
