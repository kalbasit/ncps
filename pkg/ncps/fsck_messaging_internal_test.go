package ncps

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idsOf extracts the stable phase ids from a slice of fsckCheck in order.
func idsOf(checks []fsckCheck) []string {
	out := make([]string, len(checks))
	for i, c := range checks {
		out[i] = c.id
	}

	return out
}

func TestActiveFsckPhase1Checks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		cdcMode       bool
		verifyContent bool
		want          []string
	}{
		{
			name:          "no CDC runs only the always-on checks",
			cdcMode:       false,
			verifyContent: false,
			want:          []string{"1a", "1b", "1c", "1d"},
		},
		{
			name:          "CDC adds the chunk checks",
			cdcMode:       true,
			verifyContent: false,
			want:          []string{"1a", "1b", "1c", "1d", "1e", "1f", "1g", "1h"},
		},
		{
			name:          "CDC plus verify-content adds the content checks",
			cdcMode:       true,
			verifyContent: true,
			want:          []string{"1a", "1b", "1c", "1d", "1e", "1f", "1g", "1h", "1i", "1j"},
		},
		{
			name:          "verify-content without CDC stays at the always-on checks",
			cdcMode:       false,
			verifyContent: true,
			want:          []string{"1a", "1b", "1c", "1d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := idsOf(activeFsckPhase1Checks(tt.cdcMode, tt.verifyContent))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestActiveFsckPhase1ChecksMatchMasterOrder(t *testing.T) {
	t.Parallel()

	// The active subset must always preserve the master execution order.
	master := idsOf(fsckPhase1Checks)
	got := idsOf(activeFsckPhase1Checks(true, true))
	assert.Equal(t, master, got, "all checks active should equal the master list in order")

	// Every active id must appear in the same relative order as the master list.
	subset := idsOf(activeFsckPhase1Checks(true, false))

	var lastIdx int

	for _, id := range subset {
		idx := indexOf(master, id)
		require.GreaterOrEqual(t, idx, 0, "active id %q missing from master list", id)
		assert.GreaterOrEqual(t, idx, lastIdx, "active ids out of master order at %q", id)
		lastIdx = idx
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}

	return -1
}

func TestFsckRunModeOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		dryRun bool
		repair bool
		want   fsckRunMode
	}{
		{"neither flag prompts", false, false, fsckModeReport},
		{"repair repairs", false, true, fsckModeRepair},
		{"dry-run reports only", true, false, fsckModeDryRun},
		{"dry-run dominates repair", true, true, fsckModeDryRun},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, fsckRunModeOf(tt.dryRun, tt.repair))
		})
	}
}

func TestPrintFsckRunPlanNonCDC(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	printFsckRunPlan(&buf, false, false, fsckModeReport)
	out := buf.String()

	// All three top-level phases are listed, in order.
	assert.Less(t, strings.Index(out, "Phase 1"), strings.Index(out, "Phase 2"))
	assert.Less(t, strings.Index(out, "Phase 2"), strings.Index(out, "Phase 3"))

	// Active always-on checks are present.
	for _, id := range []string{"1a", "1b", "1c", "1d"} {
		assert.Contains(t, out, "["+id+"]")
	}

	// CDC and verify-content checks are absent.
	for _, id := range []string{"1e", "1f", "1g", "1h", "1i", "1j"} {
		assert.NotContains(t, out, "["+id+"]")
	}

	// Report mode mentions the confirmation prompt, not an unconditional repair.
	assert.Contains(t, out, "confirm")
}

func TestPrintFsckRunPlanCDCRepair(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	printFsckRunPlan(&buf, true, false, fsckModeRepair)
	out := buf.String()

	// CDC checks now appear...
	for _, id := range []string{"1e", "1f", "1g", "1h"} {
		assert.Contains(t, out, "["+id+"]")
	}
	// ...but content checks still do not (verify-content is off).
	for _, id := range []string{"1i", "1j"} {
		assert.NotContains(t, out, "["+id+"]")
	}

	assert.Contains(t, out, "Repair the confirmed issues")
}

func TestPrintFsckRunPlanVerifyContentAndDryRun(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	printFsckRunPlan(&buf, true, true, fsckModeDryRun)
	out := buf.String()

	// Content checks appear with CDC + verify-content.
	for _, id := range []string{"1i", "1j"} {
		assert.Contains(t, out, "["+id+"]")
	}

	// Dry-run advertises that repair is skipped.
	assert.Contains(t, out, "SKIPPED")
	assert.Contains(t, strings.ToLower(out), "dry-run")
}

// TestLogProgressUniformFields proves the shared progress helper emits the same
// field set for every phase: phase, checked, total, percent (when total known),
// and rate. All phase tickers funnel through this helper, so the periodic update
// shape is identical across phases 1, 2, and 3.
func TestLogProgressUniformFields(t *testing.T) {
	t.Parallel()

	for _, phase := range []string{"phase 1: scanning", "phase 2: re-verifying", "phase 3: repairing"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := zerolog.New(&buf)
			// elapsed > 0 and checked > 0 so rate is emitted; checked <= total so percent is emitted.
			logProgress(logger, phase, time.Now().Add(-time.Second), 5, 10).Msg("progress update")

			var fields map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &fields))

			assert.Equal(t, phase, fields["phase"])
			assert.EqualValues(t, 5, fields["checked"])
			assert.EqualValues(t, 10, fields["total"])
			assert.Contains(t, fields, "percent")
			assert.Contains(t, fields, "rate")
		})
	}
}
