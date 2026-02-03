package golomb

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math/big"
)

var (
	// ErrInvalidGolombK is returned when the Golomb parameter k is invalid.
	ErrInvalidGolombK = errors.New("invalid golomb parameter k")
	// ErrInvalidEncodingParams is returned when encoding parameters are invalid.
	ErrInvalidEncodingParams = errors.New("invalid encoding parameters")
)

// BitWriter writes bits to an io.Writer.
type BitWriter struct {
	w           io.ByteWriter
	currentByte byte
	bitCount    uint8 // bits used in currentByte
}

// NewBitWriter creates a new BitWriter on top of an io.Writer.
// The underlying writer must support WriteByte (e.g., bufio.Writer).
func NewBitWriter(w io.ByteWriter) *BitWriter {
	return &BitWriter{w: w}
}

// WriteBit writes a single bit (0 or 1).
func (bw *BitWriter) WriteBit(bit bool) error {
	if bit {
		bw.currentByte |= (1 << (7 - bw.bitCount))
	}

	bw.bitCount++
	if bw.bitCount == 8 {
		if err := bw.w.WriteByte(bw.currentByte); err != nil {
			return err
		}

		bw.currentByte = 0
		bw.bitCount = 0
	}

	return nil
}

// WriteBits writes the n least significant bits of val, most significant first.
func (bw *BitWriter) WriteBits(val uint64, n int) error {
	for i := n - 1; i >= 0; i-- {
		bit := (val >> i) & 1
		if err := bw.WriteBit(bit == 1); err != nil {
			return err
		}
	}

	return nil
}

// Flush writes any pending bits to the underlying writer, padded with zeros.
func (bw *BitWriter) Flush() error {
	if bw.bitCount > 0 {
		return bw.w.WriteByte(bw.currentByte)
	}

	return nil
}

// BitReader reads bits from an io.ByteReader.
type BitReader struct {
	r           io.ByteReader
	currentByte byte
	bitCount    uint8 // bits available in currentByte
}

// NewBitReader creates a new BitReader on top of an io.ByteReader.
func NewBitReader(r io.ByteReader) *BitReader {
	return &BitReader{r: r}
}

// ReadBit reads a single bit.
func (br *BitReader) ReadBit() (bool, error) {
	if br.bitCount == 0 {
		b, err := br.r.ReadByte()
		if err != nil {
			return false, err
		}

		br.currentByte = b
		br.bitCount = 8
	}

	bit := (br.currentByte >> (br.bitCount - 1)) & 1
	br.bitCount--

	return bit == 1, nil
}

// ReadBits reads n bits and returns them as a uint64.
func (br *BitReader) ReadBits(n int) (uint64, error) {
	var val uint64

	for i := 0; i < n; i++ {
		bit, err := br.ReadBit()
		if err != nil {
			return 0, err
		}

		val <<= 1
		if bit {
			val |= 1
		}
	}

	return val, nil
}

// Encoder encodes integers using Golomb-Rice coding.
type Encoder struct {
	BitWriter *BitWriter
	flusher   interface {
		Flush() error
	}
	k int    // Parameter k where M = 2^k
	m uint64 // M = 2^k
}

// NewEncoder creates a new encoder.
func NewEncoder(w io.Writer, k int) (*Encoder, error) {
	// if k < 0 || k >= 64 {
	// 	return nil, fmt.Errorf("%w: %d, must be in range [0, 63]", ErrInvalidGolombK, k)
	// }

	// Ensure w implements io.ByteWriter, wrap in bufio if not
	var bw io.ByteWriter

	var flusher interface {
		Flush() error
	}

	if ibw, ok := w.(io.ByteWriter); ok {
		bw = ibw
	} else {
		bufW := bufio.NewWriter(w)
		bw = bufW
		flusher = bufW
	}

	return &Encoder{
		BitWriter: NewBitWriter(bw),
		flusher:   flusher,
		k:         k,
		m:         1 << k,
	}, nil
}

// Encode encodes a value d.
func (ge *Encoder) Encode(d uint64) error {
	q := d >> ge.k      // d / M
	r := d & (ge.m - 1) // d % M

	// Unary encode q: q ones followed by a zero
	for i := uint64(0); i < q; i++ {
		if err := ge.BitWriter.WriteBit(true); err != nil {
			return err
		}
	}

	if err := ge.BitWriter.WriteBit(false); err != nil {
		return err
	}

	// Binary encode r: k bits
	return ge.BitWriter.WriteBits(r, ge.k)
}

// Flush flushes the underlying bit writer and any buffered writer.
func (ge *Encoder) Flush() error {
	if err := ge.BitWriter.Flush(); err != nil {
		return err
	}

	if ge.flusher != nil {
		return ge.flusher.Flush()
	}

	return nil
}

// WriteBigIntBits writes the n least significant bits of val, most significant first.
func (bw *BitWriter) WriteBigIntBits(val *big.Int, n int) error {
	// We want to write bit (n-1) down to 0.
	for i := n - 1; i >= 0; i-- {
		bit := val.Bit(i) // .Bit(i) returns the bit at position i (0 is LSB)
		if err := bw.WriteBit(bit == 1); err != nil {
			return err
		}
	}

	return nil
}

// ReadBigIntBits reads n bits and returns them as a big.Int.
func (br *BitReader) ReadBigIntBits(n int) (*big.Int, error) {
	val := new(big.Int)

	for i := 0; i < n; i++ {
		bit, err := br.ReadBit()
		if err != nil {
			return nil, err
		}

		val.Lsh(val, 1) // Shift left

		if bit {
			val.SetBit(val, 0, 1) // Set LSB to 1
		}
	}

	return val, nil
}

// Decoder decodes integers using Golomb-Rice coding.
type Decoder struct {
	br *BitReader
	k  int
	m  uint64
}

// NewDecoder creates a new decoder.
func NewDecoder(r io.ByteReader, k int) (*Decoder, error) {
	if k < 0 || k >= 64 {
		return nil, fmt.Errorf("%w: %d, must be in range [0, 63]", ErrInvalidGolombK, k)
	}

	return &Decoder{
		br: NewBitReader(r),
		k:  k,
		m:  1 << k,
	}, nil
}

// Decode decodes a value.
func (gd *Decoder) Decode() (uint64, error) {
	// Decode unary q: count ones until zero
	var q uint64

	for {
		bit, err := gd.br.ReadBit()
		if err != nil {
			return 0, err
		}

		if !bit {
			break
		}

		q++
	}

	// Decode binary r: k bits
	r, err := gd.br.ReadBits(gd.k)
	if err != nil {
		return 0, err
	}

	return q*gd.m + r, nil
}

// EncodeBig encodes a big.Int delta.
func (ge *Encoder) EncodeBig(d *big.Int) error {
	// q = d >> k
	// r = d & (m - 1)  <-- m is 2^k, so this is d & ( (1<<k) - 1 ) i.e. lowest k bits.

	// Calculate q
	q := new(big.Int).Rsh(d, uint(ge.k)) //nolint:gosec

	// Check if q fits in standard memory constraint?
	// Unary encoding q means writing q ones.
	// If q is huge, we will write huge amount of data.
	// But assuming k is chosen well, q should be small.
	// We iterate q times.

	// Since we don't want to loop 2^64 times if q is huge, we can use a loop.
	// However, q *should* be small.
	// Go's big.Int doesn't support "iterate up to value" easily without loop/cmp.
	// But we can check BitLen.

	// Write q ones
	if q.IsUint64() {
		qVal := q.Uint64()
		for i := uint64(0); i < qVal; i++ {
			if err := ge.BitWriter.WriteBit(true); err != nil {
				return err
			}
		}
	} else {
		// This path is for q > 2^64, which will produce an enormous output
		// and be extremely slow. It's kept for correctness with very large numbers,
		// but in practice, `k` should be chosen to keep `q` small.
		zero := big.NewInt(0)
		one := big.NewInt(1)

		currQ := new(big.Int).Set(q)
		for currQ.Cmp(zero) > 0 {
			if err := ge.BitWriter.WriteBit(true); err != nil {
				return err
			}

			currQ.Sub(currQ, one)
		}
	}

	// Write zero delimiter
	if err := ge.BitWriter.WriteBit(false); err != nil {
		return err
	}

	// Write r (k bits). r is the lowest k bits of d.
	// We can use WriteBigIntBits on d directly, taking 'k' bits.
	return ge.BitWriter.WriteBigIntBits(d, ge.k)
}

// DecodeBig decodes a value as big.Int.
func (gd *Decoder) DecodeBig() (*big.Int, error) {
	// Decode unary q
	q := new(big.Int)

	var qUint64 uint64

	useBigInt := false

	for {
		bit, err := gd.br.ReadBit()
		if err != nil {
			return nil, err
		}

		if !bit {
			break
		}

		if useBigInt {
			q.Add(q, big.NewInt(1)) // This is slow, but q is already huge.
		} else if qUint64 < ^uint64(0) {
			qUint64++
		} else {
			useBigInt = true

			q.SetUint64(qUint64)
			q.Add(q, big.NewInt(1))
		}
	}

	if !useBigInt {
		q.SetUint64(qUint64)
	}

	// Decode binary r: k bits
	r, err := gd.br.ReadBigIntBits(gd.k)
	if err != nil {
		return nil, err
	}

	// d = q * M + r
	// M = 2^k
	// d = (q << k) | r  (since r < 2^k)

	d := new(big.Int).Lsh(q, uint(gd.k)) //nolint:gosec
	d.Or(d, r)

	return d, nil
}
