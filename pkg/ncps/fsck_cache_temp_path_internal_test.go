package ncps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

// TestFsckRegistersCacheTempPathFlag proves the fsck subcommand registers the
// cache-temp-path flag wired to the same config key and env var as serve and the
// migrate commands. Without it, fsck --repair builds a cache with an empty temp
// dir that falls back to a read-only /tmp on hardened deployments and aborts
// before any repair.
func TestFsckRegistersCacheTempPathFlag(t *testing.T) {
	t.Parallel()

	var sourceCalls [][2]string

	flagSources := func(configFileKey, envVar string) cli.ValueSourceChain {
		sourceCalls = append(sourceCalls, [2]string{configFileKey, envVar})

		return cli.ValueSourceChain{}
	}

	cmd := fsckCommand(flagSources, func(string, shutdownFn) {})

	names := make(map[string]bool)

	for _, f := range cmd.Flags {
		for _, n := range f.Names() {
			names[n] = true
		}
	}

	assert.True(t, names["cache-temp-path"], "fsck must register the --cache-temp-path flag")
	assert.Contains(
		t,
		sourceCalls,
		[2]string{"cache.temp-path", "CACHE_TEMP_PATH"},
		"cache-temp-path must be sourced from config cache.temp-path and env CACHE_TEMP_PATH",
	)
}
