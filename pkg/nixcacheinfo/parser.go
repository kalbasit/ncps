package nixcacheinfo

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// NOTE: Inspired from:
// https://github.com/nix-community/go-nix/blob/0327d78224c2de28edd957d2ef4240711217d7fe/pkg/narinfo/parser.go#L1

var (
	// ErrUnknownKey is returned if the nix-cache-info contains a key that is not known
	ErrUnknownKey = errors.New("error the key is not known")
)

// NixCacheInfo represents the nix cache info parsed.
type NixCacheInfo struct {
	StoreDir      string
	WantMassQuery uint64
	Priority      uint64
}

// ParseString parser the NixCacheInfo from a string.
func ParseString(nci string) (NixCacheInfo, error) {
	return Parse(strings.NewReader(nci))
}

// Parse parses the NixCacheInfo from an io.Reader.
func Parse(r io.Reader) (NixCacheInfo, error) {
	nci := NixCacheInfo{}
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()
		// skip empty lines (like, an empty line at EOF)
		if line == "" {
			continue
		}

		k, v, err := splitOnce(line, ": ")
		if err != nil {
			return nci, fmt.Errorf("error splitting the line by column: %w", err)
		}

		switch k {
		case "StoreDir":
			nci.StoreDir = v
		case "WantMassQuery":
			nci.WantMassQuery, err = strconv.ParseUint(v, 10, 0)
			if err != nil {
				return nci, fmt.Errorf("error parsing %q as uint64: %w", v, err)
			}
		case "Priority":
			nci.Priority, err = strconv.ParseUint(v, 10, 0)
			if err != nil {
				return nci, fmt.Errorf("error parsing %q as uint64: %w", v, err)
			}
		default:
			return nci, fmt.Errorf("%w: %q", ErrUnknownKey, k)
		}
	}

	if err := scanner.Err(); err != nil {
		return nci, fmt.Errorf("scanner error: %w", err)
	}

	return nci, nil
}
