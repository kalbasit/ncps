package nixcacheinfo

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// NOTE: Copied from:
// https://github.com/nix-community/go-nix/blob/0327d78224c2de28edd957d2ef4240711217d7fe/pkg/narinfo/parser.go#L1

// NixCacheInfo represents the nix cache info parsed.
type NixCacheInfo struct {
	StoreDir      string
	WantMassQuery uint64
	Priority      uint64
}

func ParseString(nci string) (NixCacheInfo, error) {
	return Parse(strings.NewReader(nci))
}

func Parse(r io.Reader) (NixCacheInfo, error) {
	nci := NixCacheInfo{}
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		var err error

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
				return nci, err
			}
		case "Priority":
			nci.Priority, err = strconv.ParseUint(v, 10, 0)
			if err != nil {
				return nci, err
			}
		default:
			return nci, fmt.Errorf("unknown key %v", k)
		}

		if err != nil {
			return nci, fmt.Errorf("unable to parse line %v", line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nci, err
	}

	return nci, nil
}
