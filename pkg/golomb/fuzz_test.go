package golomb_test

import (
	"bytes"
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
