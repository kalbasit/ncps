package golomb_test

import (
	"bytes"
	"io"
	"math/big"
	"testing"

	"github.com/kalbasit/ncps/pkg/golomb"
)

func FuzzDecode(f *testing.F) {
	f.Add([]byte{0x00, 0x01, 0x02}, 5)
	f.Add([]byte{0xFF, 0xFF}, 0)
	f.Add([]byte{}, 10)

	f.Fuzz(func(t *testing.T, data []byte, k int) {
		r := bytes.NewReader(data)

		dec, err := golomb.NewDecoder(r, k)
		if err != nil {
			t.Skip()
		}

		_, _ = dec.Decode()
	})
}

func FuzzDecodeBig(f *testing.F) {
	f.Add([]byte{0x00, 0x01, 0x02}, 5)
	f.Add([]byte{0xFF, 0xFF}, 0)
	f.Add([]byte{}, 10)

	f.Fuzz(func(t *testing.T, data []byte, k int) {
		r := bytes.NewReader(data)

		dec, err := golomb.NewDecoder(r, k)
		if err != nil {
			t.Skip()
		}

		_, _ = dec.DecodeBig()
	})
}

func FuzzEncode(f *testing.F) {
	f.Add(1, uint64(5))
	f.Add(0, uint64(0))
	f.Add(64, uint64(10))

	f.Fuzz(func(t *testing.T, k int, d uint64) {
		enc, err := golomb.NewEncoder(io.Discard, k)
		if err != nil {
			t.Skip()
		}

		_ = enc.Encode(d)
	})
}

func FuzzEncodeBig(f *testing.F) {
	f.Add(1, int64(5))
	f.Add(0, int64(0))
	f.Add(64, int64(10))

	f.Fuzz(func(t *testing.T, k int, d int64) {
		enc, err := golomb.NewEncoder(io.Discard, k)
		if err != nil {
			t.Skip()
		}

		_ = enc.EncodeBig(big.NewInt(d))
	})
}
