package nixcacheindex

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// JournalOp represents a journal operation (add or delete).
type JournalOp int

const (
	OpAdd JournalOp = iota
	OpDelete
)

// JournalEntry is a single entry in the journal.
type JournalEntry struct {
	Op   JournalOp
	Hash string // 32-char Nix base32 hash
}

// ParseJournal parses journal entries from an io.Reader.
// Format is line-based:
// +<hash>
// -<hash>
// Empty lines are ignored.
func ParseJournal(r io.Reader) ([]JournalEntry, error) {
	scanner := bufio.NewScanner(r)

	var entries []JournalEntry

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

		var op JournalOp

		switch opChar {
		case '+':
			op = OpAdd
		case '-':
			op = OpDelete
		default:
			return nil, fmt.Errorf("%w: line %d: %q", ErrInvalidJournalOp, lineNum, opChar)
		}

		// Validate hash characters roughly (checked fully if we ParseHash later)
		// For now just length is checked above.

		entries = append(entries, JournalEntry{
			Op:   op,
			Hash: hash,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}

// WriteJournal writes journal entries to an io.Writer.
func WriteJournal(w io.Writer, entries []JournalEntry) error {
	for _, entry := range entries {
		var opChar string

		switch entry.Op {
		case OpAdd:
			opChar = "+"
		case OpDelete:
			opChar = "-"
		default:
			return fmt.Errorf("%w: %v", ErrInvalidJournalOp, entry.Op)
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
