package golomb_test

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/golomb"
)

func TestBitReaderWrite(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	bw := golomb.NewBitWriter(&buf)

	// Write 1 0 1 1 0 0 0 1
	// Byte: 10110001 = 0xB1
	assert.NoError(t, bw.WriteBit(true))
	assert.NoError(t, bw.WriteBit(false))
	assert.NoError(t, bw.WriteBits(0b11, 2))
	assert.NoError(t, bw.WriteBits(0b00, 2))
	assert.NoError(t, bw.WriteBits(0b0, 1))
	assert.NoError(t, bw.WriteBits(0b1, 1))

	assert.NoError(t, bw.Flush())
	assert.Equal(t, []byte{0xB1}, buf.Bytes())

	// Read back
	br := golomb.NewBitReader(&buf)

	b, err := br.ReadBit()
	require.NoError(t, err)
	assert.True(t, b)

	b, err = br.ReadBit()
	require.NoError(t, err)
	assert.False(t, b)

	val, err := br.ReadBits(2)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), val) // 11

	val, err = br.ReadBits(4)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), val) // 0001
}

func TestGolombRoundTrip(t *testing.T) {
	t.Parallel()

	k := 3 // M = 8
	values := []uint64{0, 1, 7, 8, 9, 100, 12345}

	var buf bytes.Buffer

	enc, err := golomb.NewEncoder(&buf, k)
	require.NoError(t, err)

	for _, v := range values {
		err := enc.Encode(v)
		require.NoError(t, err)
	}

	require.NoError(t, enc.Flush())

	dec := golomb.NewDecoder(&buf, k)
	for i, want := range values {
		got, err := dec.Decode()
		require.NoError(t, err, "failed to decode value at index %d", i)
		assert.Equal(t, want, got, "value mismatched at index %d", i)
	}
}

func TestGolombExample(t *testing.T) {
	t.Parallel()

	// Example from RFC:
	// Delta d = 1000
	// M=256, k=8
	// q = 3, r = 232
	// Expect: 1110 (unary 3) | 11101000 (binary 232)
	// Total 12 bits: 1110 1110 1000 ...
	var buf bytes.Buffer

	enc, err := golomb.NewEncoder(&buf, 8)
	require.NoError(t, err)
	err = enc.Encode(1000)
	require.NoError(t, err)

	require.NoError(t, enc.Flush())

	// 1110 1110 1000 0000 (padded) -> EE 80
	decodedBytes := buf.Bytes()
	require.Len(t, decodedBytes, 2)
	assert.Equal(t, byte(0xEE), decodedBytes[0])
	assert.Equal(t, byte(0x80), decodedBytes[1])
}

func TestGolombBigIntRoundTrip(t *testing.T) {
	t.Parallel()

	k := 60 // Use a large k to keep q small, otherwise unary encoding of 2^64 takes forever
	// Values that might exceed uint64
	values := []*big.Int{
		big.NewInt(0),
		big.NewInt(123),
		new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1)), // 2^64 - 1
		new(big.Int).Lsh(big.NewInt(1), 64),                                  // 2^64
		new(big.Int).Lsh(big.NewInt(12345), 70),
	}

	var buf bytes.Buffer

	enc, err := golomb.NewEncoder(&buf, k)
	require.NoError(t, err)

	for _, v := range values {
		err := enc.EncodeBig(v)
		require.NoError(t, err)
	}

	require.NoError(t, enc.Flush())

	dec := golomb.NewDecoder(&buf, k)
	for i, want := range values {
		got, err := dec.DecodeBig()
		require.NoError(t, err, "failed to decode big.Int value at index %d", i)
		assert.Equal(t, 0, want.Cmp(got),
			"big.Int value mismatched at index %d, want: %s, got: %s", i, want.String(), got.String())
	}
}
