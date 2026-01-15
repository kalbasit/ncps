package nixcacheindex_test

import (
	"bytes"
	"math/big"
	"reflect"
	"testing"

	"github.com/kalbasit/ncps/pkg/nixcacheindex"
)

func TestGenerateDeltas(t *testing.T) {
	t.Parallel()

	// Helper to create big.Ints
	h1 := big.NewInt(1)
	h2 := big.NewInt(2)
	h3 := big.NewInt(3)
	h4 := big.NewInt(4)

	tests := []struct {
		name      string
		oldHashes []*big.Int
		newHashes []*big.Int
		want      []nixcacheindex.DeltaEntry
	}{
		{
			name:      "Empty to Empty",
			oldHashes: []*big.Int{},
			newHashes: []*big.Int{},
			want:      nil,
		},
		{
			name:      "No change",
			oldHashes: []*big.Int{h1, h2},
			newHashes: []*big.Int{h1, h2},
			want:      nil,
		},
		{
			name:      "Additions only",
			oldHashes: []*big.Int{h1},
			newHashes: []*big.Int{h1, h2, h3},
			want: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOpAdd, Hash: nixcacheindex.FormatHash(h2)},
				{Op: nixcacheindex.DeltaOpAdd, Hash: nixcacheindex.FormatHash(h3)},
			},
		},
		{
			name:      "Deletions only",
			oldHashes: []*big.Int{h1, h2, h3},
			newHashes: []*big.Int{h1},
			want: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOpDelete, Hash: nixcacheindex.FormatHash(h2)},
				{Op: nixcacheindex.DeltaOpDelete, Hash: nixcacheindex.FormatHash(h3)},
			},
		},
		{
			name:      "Mixed",
			oldHashes: []*big.Int{h1, h2},
			newHashes: []*big.Int{h2, h3},
			want: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOpDelete, Hash: nixcacheindex.FormatHash(h1)},
				{Op: nixcacheindex.DeltaOpAdd, Hash: nixcacheindex.FormatHash(h3)},
			},
		},
		{
			name:      "Complete Change",
			oldHashes: []*big.Int{h1, h2},
			newHashes: []*big.Int{h3, h4},
			want: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOpDelete, Hash: nixcacheindex.FormatHash(h1)},
				{Op: nixcacheindex.DeltaOpDelete, Hash: nixcacheindex.FormatHash(h2)},
				{Op: nixcacheindex.DeltaOpAdd, Hash: nixcacheindex.FormatHash(h3)},
				{Op: nixcacheindex.DeltaOpAdd, Hash: nixcacheindex.FormatHash(h4)},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := nixcacheindex.GenerateDeltas(tt.oldHashes, tt.newHashes)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GenerateDeltas() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyDelta(t *testing.T) {
	t.Parallel()

	h1 := big.NewInt(1)
	h2 := big.NewInt(2)
	h3 := big.NewInt(3)

	tests := []struct {
		name      string
		oldHashes []*big.Int
		delta     []nixcacheindex.DeltaEntry
		want      []*big.Int
		wantErr   bool
	}{
		{
			name:      "Apply Add",
			oldHashes: []*big.Int{h1},
			delta: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOpAdd, Hash: nixcacheindex.FormatHash(h2)},
			},
			want: []*big.Int{h1, h2},
		},
		{
			name:      "Apply Add In Middle",
			oldHashes: []*big.Int{h1, h3},
			delta: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOpAdd, Hash: nixcacheindex.FormatHash(h2)},
			},
			want: []*big.Int{h1, h2, h3},
		},
		{
			name:      "Apply Delete",
			oldHashes: []*big.Int{h1, h2},
			delta: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOpDelete, Hash: nixcacheindex.FormatHash(h2)},
			},
			want: []*big.Int{h1},
		},
		{
			name:      "Apply Invalid Delete",
			oldHashes: []*big.Int{h1},
			delta: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOpDelete, Hash: nixcacheindex.FormatHash(h2)},
			},
			wantErr: true,
		},
		{
			name:      "Mixed",
			oldHashes: []*big.Int{h1, h2},
			delta: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOpDelete, Hash: nixcacheindex.FormatHash(h1)},
				{Op: nixcacheindex.DeltaOpAdd, Hash: nixcacheindex.FormatHash(h3)},
			},
			want: []*big.Int{h2, h3},
		},
		{
			name:      "Apply Invalid Op",
			oldHashes: []*big.Int{h1},
			delta: []nixcacheindex.DeltaEntry{
				{Op: nixcacheindex.DeltaOp(999), Hash: nixcacheindex.FormatHash(h1)},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := nixcacheindex.ApplyDelta(tt.oldHashes, tt.delta)
			if (err != nil) != tt.wantErr {
				t.Errorf("ApplyDelta() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ApplyDelta() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadWriteDelta(t *testing.T) {
	t.Parallel()

	entries := []nixcacheindex.DeltaEntry{
		{Op: nixcacheindex.DeltaOpDelete, Hash: nixcacheindex.FormatHash(big.NewInt(100))},
		{Op: nixcacheindex.DeltaOpAdd, Hash: nixcacheindex.FormatHash(big.NewInt(200))},
	}

	var buf bytes.Buffer

	err := nixcacheindex.WriteDelta(&buf, entries)
	if err != nil {
		t.Fatalf("WriteDelta failed: %v", err)
	}

	readEntries, err := nixcacheindex.ParseDelta(&buf)
	if err != nil {
		t.Fatalf("ParseDelta failed: %v", err)
	}

	if !reflect.DeepEqual(entries, readEntries) {
		t.Errorf("Read/Write mismatch. Got %v, want %v", readEntries, entries)
	}
}
