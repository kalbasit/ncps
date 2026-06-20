package ncps

import (
	"bytes"
	"encoding/json"
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

	// Assert the exact run plan: no CDC mode → only the always-on phase-1 checks,
	// and report mode → the "confirm at the prompt" Phase 3 line.
	want := "\n" +
		"ncps fsck — planned phases for this run:\n" +
		"\n" +
		"  Phase 1 — Scan the database and storage for inconsistencies:\n" +
		"      [1a] narinfos with no linked nar_file\n" +
		"      [1b] orphaned nar_files in the database\n" +
		"      [1c] nar_files missing from storage\n" +
		"      [1d] orphaned NAR files in storage\n" +
		"  Phase 2 — Re-verify suspected issues to rule out in-flight operations\n" +
		"  Phase 3 — Repair: runs only if you confirm at the prompt\n" +
		"\n"
	assert.Equal(t, want, buf.String())
}

func TestPrintFsckRunPlanCDCRepair(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	printFsckRunPlan(&buf, true, false, fsckModeRepair)

	// Assert the exact run plan: CDC mode adds the chunk checks (1e–1h), but
	// without --verify-content the content checks (1i/1j) stay out; repair mode →
	// the "Repair the confirmed issues" Phase 3 line.
	want := "\n" +
		"ncps fsck — planned phases for this run:\n" +
		"\n" +
		"  Phase 1 — Scan the database and storage for inconsistencies:\n" +
		"      [1a] narinfos with no linked nar_file\n" +
		"      [1b] orphaned nar_files in the database\n" +
		"      [1c] nar_files missing from storage\n" +
		"      [1d] orphaned NAR files in storage\n" +
		"      [1e] orphaned chunks in the database\n" +
		"      [1f] NAR files with chunk issues\n" +
		"      [1g] CDC NAR files with size mismatch\n" +
		"      [1h] orphaned chunk files in storage\n" +
		"  Phase 2 — Re-verify suspected issues to rule out in-flight operations\n" +
		"  Phase 3 — Repair the confirmed issues\n" +
		"\n"
	assert.Equal(t, want, buf.String())
}

func TestPrintFsckRunPlanVerifyContentAndDryRun(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	printFsckRunPlan(&buf, true, true, fsckModeDryRun)

	// Assert the exact run plan: CDC + --verify-content adds the content checks
	// (1i/1j); dry-run mode → the "SKIPPED" Phase 3 line.
	want := "\n" +
		"ncps fsck — planned phases for this run:\n" +
		"\n" +
		"  Phase 1 — Scan the database and storage for inconsistencies:\n" +
		"      [1a] narinfos with no linked nar_file\n" +
		"      [1b] orphaned nar_files in the database\n" +
		"      [1c] nar_files missing from storage\n" +
		"      [1d] orphaned NAR files in storage\n" +
		"      [1e] orphaned chunks in the database\n" +
		"      [1f] NAR files with chunk issues\n" +
		"      [1g] CDC NAR files with size mismatch\n" +
		"      [1h] orphaned chunk files in storage\n" +
		"      [1i] corrupt chunk content\n" +
		"      [1j] assembled NAR hash mismatch\n" +
		"  Phase 2 — Re-verify suspected issues to rule out in-flight operations\n" +
		"  Phase 3 — Repair: SKIPPED (--dry-run reports issues without changing anything)\n" +
		"\n"
	assert.Equal(t, want, buf.String())
}

// TestLogProgressUniformFields proves the shared progress helper emits the same
// field set for every phase: phase, checked, total, percent (when total known),
// and rate. All phase tickers funnel through this helper, so the periodic update
// shape is identical across phases 1, 2, and 3.
func TestLogProgressUniformFields(t *testing.T) {
	t.Parallel()

	for _, phase := range []string{"1c", "2", "3"} {
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

// TestLogProgressOmitsUnknownTotal proves that when the work size is not known up
// front (total <= 0, e.g. a chunk-storage walk), the helper omits both total and
// percent rather than emitting a frozen denominator that `checked` overruns. This
// is the fix for the confusing "checked=128299 total=86479" phase-1h output.
func TestLogProgressOmitsUnknownTotal(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := zerolog.New(&buf)
	logProgress(logger, "1h", time.Now().Add(-time.Second), 128299, 0).
		Str("check", "orphaned chunk files in storage").
		Msg("progress update")

	var fields map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &fields))

	assert.Equal(t, "1h", fields["phase"])
	assert.Equal(t, "orphaned chunk files in storage", fields["check"])
	assert.EqualValues(t, 128299, fields["checked"])
	assert.NotContains(t, fields, "total")
	assert.NotContains(t, fields, "percent")
	assert.Contains(t, fields, "rate")
}
