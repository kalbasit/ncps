package helper

import (
	"errors"
	"regexp"
)

var (
	// ErrNoMatchesInURL is returned if the regexp did not match the given URL.
	ErrNoMatchesInURL = errors.New("no matches were found in the URL")

	narRegexp = regexp.MustCompile(`^nar/([a-z0-9]+)\.nar(?:\.([a-z0-9]+))?$`)
)

// ParseNarURL parses a nar URL (as present in narinfo) and returns its components.
func ParseNarURL(URL string) (string, string, error) {
	if URL == "" {
		return "", "", ErrNoMatchesInURL
	}

	sm := narRegexp.FindStringSubmatch(URL)
	if len(sm) != 3 {
		return "", "", ErrNoMatchesInURL
	}

	return sm[1], sm[2], nil
}
