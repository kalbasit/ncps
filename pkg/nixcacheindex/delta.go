package nixcacheindex

import (
	"bufio"
	"fmt"
	"io"
	"math/big"
	"strings"
)

// DeltaOp represents a delta operation (add or delete).
type DeltaOp int

const (
	DeltaOpAdd DeltaOp = iota
	DeltaOpDelete
)

// DeltaEntry is a single entry in the delta file.
type DeltaEntry struct {
	Op   DeltaOp
	Hash string // 32-char Nix base32 hash
}

// ChecksumFile represents the checksums/ metadata file for an epoch.
type ChecksumFile struct {
	Epoch     int                      `json:"epoch"`
	Algorithm string                   `json:"algorithm"` // e.g. "xxh64"
	Shards    map[string]ShardChecksum `json:"shards"`    // Key is shard prefix (e.g. "b6")
}

// ShardChecksum contains verification data for a single shard.
type ShardChecksum struct {
	Checksum  string `json:"checksum"`   // Hex-encoded checksum
	ItemCount uint64 `json:"item_count"` //nolint:tagliatelle // RFC 0195
	SizeBytes uint64 `json:"size_bytes"` //nolint:tagliatelle // RFC 0195
}

// ParseDelta parses delta entries from an io.Reader.
// Format is line-based:
// +<hash>
// -<hash>
// Ops must be sorted by hash.
func ParseDelta(r io.Reader) ([]DeltaEntry, error) {
	scanner := bufio.NewScanner(r)

	var entries []DeltaEntry

	var lastHash string

	lineNum := 0

	for scanner.Scan() {
		lineNum++

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if len(line) != HashLength+1 {
			return nil, fmt.Errorf("%w: line %d: got %d (expected %d)", ErrInvalidHashLength, lineNum, len(line), HashLength+1)
		}

		opChar := line[0]
		hash := line[1:]

		var op DeltaOp

		switch opChar {
		case '+':
			op = DeltaOpAdd
		case '-':
			op = DeltaOpDelete
		default:
			return nil, fmt.Errorf("%w: line %d: %q", ErrInvalidDeltaOp, lineNum, opChar)
		}

		// Verify sorting
		if len(entries) > 0 {
			if hash < lastHash {
				return nil, fmt.Errorf("%w: line %d: %s < %s", ErrDeltaNotSorted, lineNum, hash, lastHash)
			}
		}

		lastHash = hash

		entries = append(entries, DeltaEntry{
			Op:   op,
			Hash: hash,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}

// WriteDelta writes delta entries to an io.Writer.
func WriteDelta(w io.Writer, entries []DeltaEntry) error {
	for _, entry := range entries {
		var opChar string

		switch entry.Op {
		case DeltaOpAdd:
			opChar = "+"
		case DeltaOpDelete:
			opChar = "-"
		default:
			return fmt.Errorf("%w: %v", ErrInvalidDeltaOp, entry.Op)
		}

		if len(entry.Hash) != HashLength {
			return fmt.Errorf("%w: %d (expected %d)", ErrInvalidHashLength, len(entry.Hash), HashLength)
		}

		_, err := fmt.Fprintf(w, "%s%s\n", opChar, entry.Hash)
		if err != nil {
			return err
		}
	}

	return nil
}

// GenerateDeltas computes the operations needed to transform oldHashes to newHashes.
// Both inputs must be sorted unique lists of hashes.
func GenerateDeltas(oldHashes, newHashes []*big.Int) []DeltaEntry {
	var deltas []DeltaEntry

	i, j := 0, 0
	for i < len(oldHashes) && j < len(newHashes) {
		cmp := oldHashes[i].Cmp(newHashes[j])

		if cmp < 0 {
			// Old hash not in new -> Deleted
			deltas = append(deltas, DeltaEntry{
				Op:   DeltaOpDelete,
				Hash: FormatHash(oldHashes[i]),
			})

			i++
		} else if cmp > 0 {
			// New hash not in old -> Added
			deltas = append(deltas, DeltaEntry{
				Op:   DeltaOpAdd,
				Hash: FormatHash(newHashes[j]),
			})

			j++
		} else {
			// Equal -> Present in both
			i++
			j++
		}
	}

	// Remaining old -> Deleted
	for i < len(oldHashes) {
		deltas = append(deltas, DeltaEntry{
			Op:   DeltaOpDelete,
			Hash: FormatHash(oldHashes[i]),
		})

		i++
	}

	// Remaining new -> Added
	for j < len(newHashes) {
		deltas = append(deltas, DeltaEntry{
			Op:   DeltaOpAdd,
			Hash: FormatHash(newHashes[j]),
		})

		j++
	}

	return deltas
}

// ApplyDelta applies a list of delta operations to a sorted list of hashes.
// Returns the new sorted list.
// oldHashes must be sorted. delta must be sorted by hash.
func ApplyDelta(oldHashes []*big.Int, delta []DeltaEntry) ([]*big.Int, error) { //nolint:cyclop
	newHashes := make([]*big.Int, 0, len(oldHashes)+len(delta))

	i, j := 0, 0

	// We need to parse delta hashes to *big.Int for comparison
	// Optimization: Parse on demand
	var deltaHash *big.Int

	var err error

	for i < len(oldHashes) && j < len(delta) {
		if deltaHash == nil {
			deltaHash, err = ParseHash(delta[j].Hash)
			if err != nil {
				return nil, fmt.Errorf("invalid hash in delta at index %d: %w", j, err)
			}
		}

		oldHash := oldHashes[i]
		cmp := oldHash.Cmp(deltaHash)

		if cmp < 0 {
			// Old hash is smaller than next delta op target.
			// Means old hash is unaffected by this op.
			newHashes = append(newHashes, oldHash)

			i++
		} else if cmp > 0 {
			// Delta op targets a hash smaller than current old hash.
			// Must be an ADD.
			switch delta[j].Op {
			case DeltaOpAdd:
				newHashes = append(newHashes, deltaHash)
				deltaHash = nil // Force next parse
				j++
			case DeltaOpDelete:
				return nil, fmt.Errorf("%w: delta tries to delete hash %s", ErrHashNotFound, delta[j].Hash)
			default:
				return nil, fmt.Errorf("%w: %v", ErrInvalidDeltaOp, delta[j].Op)
			}
		} else {
			// Equal.
			switch delta[j].Op {
			case DeltaOpAdd:
				// Treat as no-op/update.
				newHashes = append(newHashes, oldHash)
			case DeltaOpDelete:
				// If Delete, don't add.
			default:
				return nil, fmt.Errorf("%w: %v", ErrInvalidDeltaOp, delta[j].Op)
			}

			i++
			deltaHash = nil
			j++
		}
	}

	// Remaining old hashes
	newHashes = append(newHashes, oldHashes[i:]...)

	// Remaining delta ops
	for j < len(delta) {
		if deltaHash == nil {
			deltaHash, err = ParseHash(delta[j].Hash)
			if err != nil {
				return nil, fmt.Errorf("invalid hash in delta at index %d: %w", j, err)
			}
		}

		switch delta[j].Op {
		case DeltaOpAdd:
			newHashes = append(newHashes, deltaHash)
		case DeltaOpDelete:
			return nil, fmt.Errorf("%w: delta tries to delete hash %s (at end)", ErrHashNotFound, delta[j].Hash)
		default:
			return nil, fmt.Errorf("%w: %v", ErrInvalidDeltaOp, delta[j].Op)
		}

		deltaHash = nil
		j++
	}

	return newHashes, nil
}
