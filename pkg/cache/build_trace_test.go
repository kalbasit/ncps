package cache_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/storage"
)

// ── Fingerprint unit tests ──────────────────────────────────────────────────

func TestBuildTraceFingerprint(t *testing.T) {
	t.Parallel()

	t.Run("standard entry: signatures stripped from fingerprint", func(t *testing.T) {
		t.Parallel()

		entry := cache.BuildTraceEntryJSON{
			Key: cache.BuildTraceKey{
				DrvPath:    "/nix/store/abc-foo.drv",
				OutputName: "out",
			},
			Value: cache.BuildTraceValue{
				OutPath: "/nix/store/xyz-foo",
				Signatures: []cache.BuildTraceSig{
					{KeyName: "cache.example.com-1", Sig: "abc123"},
				},
			},
		}

		fp, err := cache.BuildTraceFingerprint(entry)
		require.NoError(t, err)

		// Fingerprint must not contain the signature.
		assert.NotContains(t, fp, "abc123")
		assert.NotContains(t, fp, "signatures")

		// Must be valid JSON and contain key + value.out_path.
		var decoded map[string]any
		require.NoError(t, json.Unmarshal([]byte(fp), &decoded))
		assert.Contains(t, fp, "/nix/store/abc-foo.drv")
		assert.Contains(t, fp, "/nix/store/xyz-foo")
	})

	t.Run("multiple existing signatures all stripped", func(t *testing.T) {
		t.Parallel()

		entry := cache.BuildTraceEntryJSON{
			Key: cache.BuildTraceKey{DrvPath: "drv.drv", OutputName: "dev"},
			Value: cache.BuildTraceValue{
				OutPath: "/nix/store/out",
				Signatures: []cache.BuildTraceSig{
					{KeyName: "key1", Sig: "sig1"},
					{KeyName: "key2", Sig: "sig2"},
				},
			},
		}

		fp, err := cache.BuildTraceFingerprint(entry)
		require.NoError(t, err)
		assert.NotContains(t, fp, "sig1")
		assert.NotContains(t, fp, "sig2")
		assert.NotContains(t, fp, "signatures")
	})

	t.Run("empty signatures field produces same fingerprint", func(t *testing.T) {
		t.Parallel()

		withSigs := cache.BuildTraceEntryJSON{
			Key:   cache.BuildTraceKey{DrvPath: "drv.drv", OutputName: "out"},
			Value: cache.BuildTraceValue{OutPath: "/nix/store/out", Signatures: []cache.BuildTraceSig{{KeyName: "k", Sig: "s"}}},
		}
		withoutSigs := cache.BuildTraceEntryJSON{
			Key:   cache.BuildTraceKey{DrvPath: "drv.drv", OutputName: "out"},
			Value: cache.BuildTraceValue{OutPath: "/nix/store/out"},
		}

		fp1, err := cache.BuildTraceFingerprint(withSigs)
		require.NoError(t, err)

		fp2, err := cache.BuildTraceFingerprint(withoutSigs)
		require.NoError(t, err)

		assert.Equal(t, fp1, fp2)
	})
}

// ── Cache integration tests ─────────────────────────────────────────────────

func testBuildTrace(factory cacheFactory) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		t.Parallel()

		const (
			drvName    = "qwwz2sxy84n4slkyff4jirbihqk3qvhf-skopeo-1.21.0.drv"
			outputName = "out"
			outPath    = "/nix/store/xyz000000000000000000000000000000-skopeo-1.21.0"
		)

		validBody := func(extraSig *cache.BuildTraceSig) string {
			sigs := []cache.BuildTraceSig{}
			if extraSig != nil {
				sigs = append(sigs, *extraSig)
			}

			entry := cache.BuildTraceEntryJSON{
				Key: cache.BuildTraceKey{
					DrvPath:    "/nix/store/" + drvName,
					OutputName: outputName,
				},
				Value: cache.BuildTraceValue{
					OutPath:    outPath,
					Signatures: sigs,
				},
			}

			b, err := json.Marshal(entry)
			if err != nil {
				panic(err)
			}

			return string(b)
		}

		t.Run("HasBuildTrace: false before any PUT", func(t *testing.T) {
			t.Parallel()

			c, _, _, _, _, cleanup := factory(t)
			defer cleanup()

			assert.False(t, c.HasBuildTrace(t.Context(), drvName, outputName))
		})

		t.Run("PutBuildTrace: success then HasBuildTrace true", func(t *testing.T) {
			t.Parallel()

			c, _, _, _, _, cleanup := factory(t)
			defer cleanup()

			body := validBody(nil)
			err := c.PutBuildTrace(t.Context(), drvName, outputName, strings.NewReader(body))
			require.NoError(t, err)
			assert.True(t, c.HasBuildTrace(t.Context(), drvName, outputName))
		})

		t.Run("GetBuildTrace: not found returns ErrNotFound", func(t *testing.T) {
			t.Parallel()

			c, _, _, _, _, cleanup := factory(t)
			defer cleanup()

			_, err := c.GetBuildTrace(t.Context(), drvName, outputName)
			assert.ErrorIs(t, err, storage.ErrNotFound)
		})

		t.Run("GetBuildTrace: ncps signature appended", func(t *testing.T) {
			t.Parallel()

			c, _, _, _, _, cleanup := factory(t)
			defer cleanup()

			upstreamSig := cache.BuildTraceSig{KeyName: "upstream-key-1", Sig: "dXBzdHJlYW0="}
			body := validBody(&upstreamSig)
			require.NoError(t, c.PutBuildTrace(t.Context(), drvName, outputName, strings.NewReader(body)))

			got, err := c.GetBuildTrace(t.Context(), drvName, outputName)
			require.NoError(t, err)

			var result cache.BuildTraceEntryJSON
			require.NoError(t, json.Unmarshal(got, &result))

			assert.Equal(t, "/nix/store/"+drvName, result.Key.DrvPath)
			assert.Equal(t, outputName, result.Key.OutputName)
			assert.Equal(t, outPath, result.Value.OutPath)

			// Both upstream and ncps signatures must be present.
			require.GreaterOrEqual(t, len(result.Value.Signatures), 2, "expected upstream + ncps signatures")

			keyNames := make([]string, len(result.Value.Signatures))
			for i, s := range result.Value.Signatures {
				keyNames[i] = s.KeyName
			}

			assert.Contains(t, keyNames, "upstream-key-1")
			assert.Contains(t, keyNames, cacheName, "ncps hostname signature must be present")
		})

		t.Run("PutBuildTrace: duplicate upsert replaces entry", func(t *testing.T) {
			t.Parallel()

			c, _, _, _, _, cleanup := factory(t)
			defer cleanup()

			body1 := validBody(nil)
			require.NoError(t, c.PutBuildTrace(t.Context(), drvName, outputName, strings.NewReader(body1)))

			// Second PUT with a different out_path (edge-case: upstream non-determinism).
			newOutPath := "/nix/store/yyy000000000000000000000000000000-skopeo-1.21.0"
			entry2 := cache.BuildTraceEntryJSON{
				Key:   cache.BuildTraceKey{DrvPath: "/nix/store/" + drvName, OutputName: outputName},
				Value: cache.BuildTraceValue{OutPath: newOutPath},
			}

			b2, err := json.Marshal(entry2)
			require.NoError(t, err)
			require.NoError(t, c.PutBuildTrace(t.Context(), drvName, outputName, strings.NewReader(string(b2))))

			got, err := c.GetBuildTrace(t.Context(), drvName, outputName)
			require.NoError(t, err)

			var result cache.BuildTraceEntryJSON
			require.NoError(t, json.Unmarshal(got, &result))
			assert.Equal(t, newOutPath, result.Value.OutPath)
		})

		t.Run("PutBuildTrace: malformed JSON returns error", func(t *testing.T) {
			t.Parallel()

			c, _, _, _, _, cleanup := factory(t)
			defer cleanup()

			err := c.PutBuildTrace(t.Context(), drvName, outputName, strings.NewReader("not json"))
			require.Error(t, err)
			assert.ErrorIs(t, err, cache.ErrBadRequest)
		})

		t.Run("PutBuildTrace: URL/body drvName mismatch returns error", func(t *testing.T) {
			t.Parallel()

			c, _, _, _, _, cleanup := factory(t)
			defer cleanup()

			body := validBody(nil) // body has drvName in key.drvPath
			err := c.PutBuildTrace(t.Context(), "wrong-name.drv", outputName, strings.NewReader(body))
			require.Error(t, err)
			assert.ErrorIs(t, err, cache.ErrBadRequest)
		})

		t.Run("PutBuildTrace: URL/body outputName mismatch returns error", func(t *testing.T) {
			t.Parallel()

			c, _, _, _, _, cleanup := factory(t)
			defer cleanup()

			body := validBody(nil) // body has outputName "out"
			err := c.PutBuildTrace(t.Context(), drvName, "dev", strings.NewReader(body))
			require.Error(t, err)
			assert.ErrorIs(t, err, cache.ErrBadRequest)
		})
	}
}
