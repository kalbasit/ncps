package oidc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"
)

var (
	// ErrMissingIssuer is returned when a policy config has no issuer.
	ErrMissingIssuer = errors.New("oidc policy must have a non-empty issuer")

	// ErrMissingAudience is returned when a policy config has no audience.
	ErrMissingAudience = errors.New("oidc policy must have a non-empty audience")

	// ErrUnsupportedFormat is returned for unsupported config format strings.
	ErrUnsupportedFormat = errors.New("unsupported config format")
)

// PolicyConfig holds the configuration for a single OIDC authorization policy.
type PolicyConfig struct {
	Issuer   string              `json:"issuer"   yaml:"issuer"   toml:"issuer"`
	Audience string              `json:"audience" yaml:"audience" toml:"audience"`
	Claims   map[string][]string `json:"claims"   yaml:"claims"   toml:"claims"`
}

// Config holds the OIDC configuration section.
type Config struct {
	Policies []PolicyConfig `json:"policies" yaml:"policies" toml:"policies"`
}

// Validate checks that all policies have required fields.
func (c *Config) Validate() error {
	for i, p := range c.Policies {
		if strings.TrimSpace(p.Issuer) == "" {
			return fmt.Errorf("policy %d: %w", i, ErrMissingIssuer)
		}

		if strings.TrimSpace(p.Audience) == "" {
			return fmt.Errorf("policy %d: %w", i, ErrMissingAudience)
		}
	}

	return nil
}

// configFile is the intermediate struct used for parsing the config file.
type configFile struct {
	Cache struct {
		OIDC *Config `json:"oidc" yaml:"oidc" toml:"oidc"`
	} `json:"cache" yaml:"cache" toml:"cache"`
}

// ParseConfigData extracts the cache.oidc section from raw config bytes.
// The format parameter should be "yaml", "toml", or "json".
// Returns nil with no error if the section is absent (backwards-compatible).
func ParseConfigData(data []byte, format string) (*Config, error) {
	if len(data) == 0 {
		return nil, nil //nolint:nilnil
	}

	var cf configFile

	switch format {
	case "yaml":
		if err := yaml.Unmarshal(data, &cf); err != nil {
			return nil, fmt.Errorf("parsing YAML config: %w", err)
		}
	case "toml":
		if err := toml.Unmarshal(data, &cf); err != nil {
			return nil, fmt.Errorf("parsing TOML config: %w", err)
		}
	case "json":
		if err := json.Unmarshal(data, &cf); err != nil {
			return nil, fmt.Errorf("parsing JSON config: %w", err)
		}
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}

	if cf.Cache.OIDC == nil || len(cf.Cache.OIDC.Policies) == 0 {
		return nil, nil //nolint:nilnil
	}

	if err := cf.Cache.OIDC.Validate(); err != nil {
		return nil, fmt.Errorf("invalid oidc config: %w", err)
	}

	return cf.Cache.OIDC, nil
}
