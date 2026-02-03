package cache_test

import (
	"bytes"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/stretchr/testify/assert"
)

func FuzzParseNarInfo(f *testing.F) {
	tests := []string{
		"",
		"StorePath: /nix/store/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps-hello-2.10",
	}

	for _, tc := range tests {
		f.Add([]byte(tc))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		ni, err := narinfo.Parse(bytes.NewReader(data))
		if err != nil {
			assert.Nil(t, ni)
		} else {
			assert.NotNil(t, ni)
			// StorePath validation depends on narinfo package strictness.
			// The main goal here is to ensure it doesn't panic.
		}
	})
}
