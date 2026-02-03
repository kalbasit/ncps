package helper_test

import (
	"testing"

	"github.com/kalbasit/ncps/pkg/helper"
)

func FuzzParseSize(f *testing.F) {
	tests := []string{
		"100M",
		"1G",
		"1024",
		"500K",
		"",
		"M",
		"100X",
	}

	for _, tc := range tests {
		f.Add(tc)
	}

	f.Fuzz(func(_ *testing.T, data string) {
		_, _ = helper.ParseSize(data)
	})
}
