package helper

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// ErrInvalidNarURL is returned if the regexp did not match the given URL.
	ErrInvalidNarURL = errors.New("invalid nar URL")

	narRegexp = regexp.MustCompile(`^nar/([a-z0-9]+)\.nar(?:\.([a-z0-9]+))?$`)
)

// ParseNarURL parses a nar URL (as present in narinfo) and returns its components.
func ParseNarURL(URL string) (string, string, error) {
	if URL == "" || !strings.HasPrefix(URL, "nar/") {
		return "", "", ErrInvalidNarURL
	}

	sm := narRegexp.FindStringSubmatch(URL)
	if len(sm) != 3 {
		return "", "", ErrInvalidNarURL
	}

	return sm[1], sm[2], nil
}
