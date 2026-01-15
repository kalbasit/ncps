package nixcacheindex

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"sort"

	"github.com/cespare/xxhash/v2"

	"github.com/kalbasit/ncps/pkg/golomb"
)

const (
	// MagicNumber is "NIXIDX01" in Little-Endian.
	// N (4E) I (49) X (58) I (49) D (44) X (58) 0 (30) 1 (31) -> 0x313058444958494E.
	MagicNumber = 0x313058444958494E

	// SparseIndexInterval is the number of items between sparse index entries.
	SparseIndexInterval = 256

	// HeaderSize is the fixed size of the shard header.
	HeaderSize = 64

	// SparseEntrySize is the size of a sparse index entry (20 + 8 = 28 bytes).
	SparseEntrySize = 28
)

// ShardHeader represents the fixed-size header of a shard file.
type ShardHeader struct {
	Magic             uint64
	ItemCount         uint64
	GolombK           uint8
	HashSuffixBits    uint8
	SparseIndexOffset uint64
	SparseIndexCount  uint64
	Checksum          uint64 // XXH64 of encoded data
	Reserved          [22]byte
}

// SparseIndexEntry represents an entry in the sparse index.
type SparseIndexEntry struct {
	HashSuffix *big.Int // 20 bytes (160 bits), store path suffix
	Offset     uint64   // 8 bytes, offset into encoded data
}

// ShardReader facilitates reading a shard.
type ShardReader struct {
	r           io.ReadSeeker
	Header      ShardHeader
	SparseIndex []SparseIndexEntry
	Params      Encoding
}

// Helper to write Little-Endian uint64.
func writeUint64(w io.Writer, v uint64) error {
	return binary.Write(w, binary.LittleEndian, v)
}

// Helper to write Little-Endian uint8.
func writeUint8(w io.Writer, v uint8) error {
	return binary.Write(w, binary.LittleEndian, v)
}

// WriteShard writes a shard to w given a list of sorted hashes.
// hashes must be sorted numerically (big-endian).
// params defines the encoding parameters (k, prefix bits).
func WriteShard(w io.Writer, hashes []*big.Int, params Encoding) error {
	if len(hashes) == 0 {
		return ErrEmptyShard
	}

	if params.Parameter < 0 || params.Parameter >= 64 {
		return fmt.Errorf("%w: %d", golomb.ErrInvalidGolombK, params.Parameter)
	}

	if params.PrefixBits < 0 || params.HashBits < params.PrefixBits {
		return fmt.Errorf("%w: prefix/hash bits %d/%d", golomb.ErrInvalidEncodingParams, params.PrefixBits, params.HashBits)
	}

	// 1. Prepare buffers
	var (
		encodedData bytes.Buffer
		sparseIndex []SparseIndexEntry
	)

	// Golomb encoder
	ge, err := golomb.NewEncoder(&encodedData, params.Parameter)
	if err != nil {
		return err
	}

	// Mask for stripping prefix
	// Hash is HashBits length. Prefix is PrefixBits. Suffix is HashBits - PrefixBits.
	suffixBits := params.HashBits - params.PrefixBits
	mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(suffixBits)), big.NewInt(1)) //nolint:gosec

	var prevSuffix *big.Int

	for i, h := range hashes {
		// Strip prefix to get suffix
		suffix := new(big.Int).And(h, mask)

		if i == 0 { //nolint:nestif
			// First hash: raw suffix bits.
			if err := ge.BitWriter.WriteBigIntBits(suffix, suffixBits); err != nil {
				return err
			}
			// Flush to byte boundary for valid Sparse Index Offset
			if err := ge.Flush(); err != nil {
				return err
			}
			// Offset is current buffer length (which points to byte immediately after hash[0])
			sparseIndex = append(sparseIndex, SparseIndexEntry{
				HashSuffix: new(big.Int).Set(suffix),
				Offset:     uint64(encodedData.Len()), //nolint:gosec
			})

			prevSuffix = suffix
		} else {
			// Delta = suffix - prev
			delta := new(big.Int).Sub(suffix, prevSuffix)

			// Encode Delta
			if err := ge.EncodeBig(delta); err != nil {
				return err
			}

			prevSuffix = suffix

			// If this is a start of a new block (e.g. 256), we flush AFTER writing it,
			// and record the offset for the NEXT item to be efficiently seekable?
			// NO. Sparse Entry 'j' corresponds to item `j*256`.
			// So Entry 1 is for item 256.
			// The entry must store `hash[256]` AND `offset` where decoding starts for `hash[257]`.
			// So we process item 256, THEN Flush, THEN record Entry 1.

			if i%SparseIndexInterval == 0 {
				if err := ge.Flush(); err != nil {
					return err
				}

				sparseIndex = append(sparseIndex, SparseIndexEntry{
					HashSuffix: new(big.Int).Set(suffix),
					Offset:     uint64(encodedData.Len()), //nolint:gosec
				})
			}
		}
	}
	// Flush final bits (though last block might not be aligned, that's fine, file ends).
	if err := ge.Flush(); err != nil {
		return err
	}

	// 2. Compute Checksum
	encodedBytes := encodedData.Bytes()
	checksum := xxhash.Sum64(encodedBytes)

	// 3. Write Header
	h := ShardHeader{
		Magic:             MagicNumber,
		ItemCount:         uint64(len(hashes)),
		GolombK:           uint8(uint(params.Parameter)), //nolint:gosec
		HashSuffixBits:    uint8(uint(suffixBits)),       //nolint:gosec
		SparseIndexOffset: uint64(HeaderSize),
		SparseIndexCount:  uint64(len(sparseIndex)),
		Checksum:          checksum,
	}

	// Write Header Fields
	if err := writeUint64(w, h.Magic); err != nil {
		return err
	}

	if err := writeUint64(w, h.ItemCount); err != nil {
		return err
	}

	if err := writeUint8(w, h.GolombK); err != nil {
		return err
	}

	if err := writeUint8(w, h.HashSuffixBits); err != nil {
		return err
	}

	if err := writeUint64(w, h.SparseIndexOffset); err != nil {
		return err
	}

	if err := writeUint64(w, h.SparseIndexCount); err != nil {
		return err
	}

	if err := writeUint64(w, h.Checksum); err != nil {
		return err
	}

	if _, err := w.Write(h.Reserved[:]); err != nil {
		return err
	}

	// 4. Write Sparse Index
	for _, entry := range sparseIndex {
		// Hash (20 bytes, Big Endian as per RFC "20 bytes, big-endian integer")
		// My WriteBigIntBits writes Big Endian bits?
		// WriteBigIntBits writes bits. `Write` probably expects bytes.
		// "Hash stored is ... 160-bit big-endian integer in 20 bytes".
		// I should convert BigInt to 20 bytes.
		b := entry.HashSuffix.Bytes()
		// Pad to 20 bytes if needed
		pad := 20 - len(b)
		if pad < 0 {
			// Should not happen if HashSuffixBits <= 160
			return fmt.Errorf("%w: %d bytes", ErrInvalidHashLength, len(b))
		}

		if pad > 0 {
			if _, err := w.Write(make([]byte, pad)); err != nil {
				return err
			}
		}

		if _, err := w.Write(b); err != nil {
			return err
		}

		// Offset (8 bytes Little Endian)
		if err := writeUint64(w, entry.Offset); err != nil {
			return err
		}
	}

	// 5. Write Encoded Data
	if _, err := w.Write(encodedBytes); err != nil {
		return err
	}

	return nil
}

// ReadShard opens and parses a shard.
func ReadShard(r io.ReadSeeker) (*ShardReader, error) {
	// Read Header
	var h ShardHeader

	b := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}

	buf := bytes.NewReader(b)

	if err := binary.Read(buf, binary.LittleEndian, &h.Magic); err != nil {
		return nil, err
	}

	if h.Magic != MagicNumber {
		return nil, fmt.Errorf("%w: %x", ErrInvalidMagic, h.Magic)
	}

	if err := binary.Read(buf, binary.LittleEndian, &h.ItemCount); err != nil {
		return nil, err
	}

	if err := binary.Read(buf, binary.LittleEndian, &h.GolombK); err != nil {
		return nil, err
	}

	if err := binary.Read(buf, binary.LittleEndian, &h.HashSuffixBits); err != nil {
		return nil, err
	}

	if err := binary.Read(buf, binary.LittleEndian, &h.SparseIndexOffset); err != nil {
		return nil, err
	}

	if err := binary.Read(buf, binary.LittleEndian, &h.SparseIndexCount); err != nil {
		return nil, err
	}

	if err := binary.Read(buf, binary.LittleEndian, &h.Checksum); err != nil {
		return nil, err
	}

	// Load Sparse Index
	if _, err := r.Seek(int64(h.SparseIndexOffset), io.SeekStart); err != nil { //nolint:gosec
		return nil, err
	}

	sparseIndex := make([]SparseIndexEntry, h.SparseIndexCount)
	for i := 0; i < int(h.SparseIndexCount); i++ { //nolint:gosec
		// Read 20 byte hash
		hashBytes := make([]byte, 20)
		if _, err := io.ReadFull(r, hashBytes); err != nil {
			return nil, err
		}

		sparseIndex[i].HashSuffix = new(big.Int).SetBytes(hashBytes)

		// Read 8 byte offset
		if err := binary.Read(r, binary.LittleEndian, &sparseIndex[i].Offset); err != nil {
			return nil, err
		}
	}

	// Verify Checksum? RFC says "Clients SHOULD verify checksum".
	// We'll skip for now to save IO or do it?
	// Reading whole encoded data into memory might be heavy. Let's do it if caller requests or assume lazy?
	// For "ReadShard", usually means "Open".
	// We won't verify checksum here to verify "Contains" performance unless we want strictness.

	return &ShardReader{
		r:           r,
		Header:      h,
		SparseIndex: sparseIndex,
		Params: Encoding{
			Parameter:  int(h.GolombK),
			HashBits:   160, // Assuming standard
			PrefixBits: 160 - int(h.HashSuffixBits),
		},
	}, nil
}

// Contains checks if the shard contains the given hash.
// The hash must match the shard's prefix (which is implicit, we only check suffix).
func (sr *ShardReader) Contains(hash *big.Int) (bool, error) {
	// Strip prefix
	bits := uint(sr.Params.HashBits - sr.Params.PrefixBits) //nolint:gosec
	mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), bits), big.NewInt(1))
	targetSuffix := new(big.Int).And(hash, mask)

	// Binary Search Sparse Index
	// Find the largest entry <= targetSuffix
	idx := sort.Search(len(sr.SparseIndex), func(i int) bool {
		return sr.SparseIndex[i].HashSuffix.Cmp(targetSuffix) > 0
	})
	// idx is the first entry > target.
	// So bracket is idx-1.

	if idx == 0 {
		// All entries > target?
		// Check if first entry IS target? (Since Search condition was >)
		// No, `Search` returns first index where f(i) is true.
		// If index 0 > target, then target is smaller than smallest sparse entry?
		// But entry 0 is the smallest element (element 0).
		// So target < element 0.
		return false, nil
	}

	bracketIdx := idx - 1
	startEntry := sr.SparseIndex[bracketIdx]

	// Optimization: check if startEntry IS target
	if startEntry.HashSuffix.Cmp(targetSuffix) == 0 {
		return true, nil
	}

	// Decode from startEntry
	// Seek to Encoded Data Start + Offset
	// Encoded Data Start is after Sparse Index.
	encodedDataStart := sr.Header.SparseIndexOffset + sr.Header.SparseIndexCount*SparseEntrySize
	seekPos := int64(encodedDataStart + startEntry.Offset) //nolint:gosec

	if _, err := sr.r.Seek(seekPos, io.SeekStart); err != nil {
		return false, err
	}

	// Golomb Decoder
	gd := golomb.NewDecoder(bufio.NewReader(sr.r), sr.Params.Parameter)

	currentHash := new(big.Int).Set(startEntry.HashSuffix)

	// Loop until current >= target
	for currentHash.Cmp(targetSuffix) < 0 {
		delta, err := gd.DecodeBig()
		if err != nil {
			if err == io.EOF {
				return false, nil
			}

			return false, err
		}

		currentHash.Add(currentHash, delta)
	}

	return currentHash.Cmp(targetSuffix) == 0, nil
}
