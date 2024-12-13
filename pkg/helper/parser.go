package helper

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	// ErrInvalidNarURL is returned if the regexp did not match the given URL.
	ErrInvalidNarURL = errors.New("invalid nar URL")

	// https://regex101.com/r/yPwxpw/2
	narRegexp = regexp.MustCompile(`^nar/([a-z0-9]+)\.nar(\.([a-z0-9]+))?(\?([a-z0-9=&]*))?$`)
)

type NarURL struct {
	Hash        string
	Compression string
	Query       url.Values
}

// ParseNarURL parses a nar URL (as present in narinfo) and returns its components.
func ParseNarURL(URL string) (NarURL, error) {
	var nu NarURL

	if URL == "" || !strings.HasPrefix(URL, "nar/") {
		return nu, ErrInvalidNarURL
	}

	sm := narRegexp.FindStringSubmatch(URL)
	if len(sm) != 6 {
		return nu, ErrInvalidNarURL
	}

	nu.Hash = sm[1]
	nu.Compression = sm[3]

	var err error
	if nu.Query, err = url.ParseQuery(sm[5]); err != nil {
		return nu, err
	}

	return nu, nil
}
