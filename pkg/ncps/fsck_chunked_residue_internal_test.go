package ncps

import (
	"testing"
	"time"
)

// TestDecideChunkedResidue exercises the pure two-tier residue policy in isolation
// from the database, covering every branch of the decision matrix.
func TestDecideChunkedResidue(t *testing.T) {
	t.Parallel()

	const (
		grace       = 24 * time.Hour
		chunkingTTL = time.Hour
	)

	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	ptr := func(d time.Duration) *time.Time {
		v := now.Add(d)

		return &v
	}

	tests := []struct {
		name              string
		resolvable        bool
		urlInconsistent   bool
		flaggedAt         *time.Time
		chunkingStartedAt *time.Time
		want              residueDecision
	}{
		{
			name:            "recoverable + inconsistent url -> normalize",
			resolvable:      true,
			urlInconsistent: true,
			want:            residueDecision{normalizeURL: true},
		},
		{
			name:       "recoverable + consistent url -> no-op (legitimately chunked)",
			resolvable: true,
			want:       residueDecision{},
		},
		{
			name:            "recoverable + previously flagged -> clear flag (and normalize)",
			resolvable:      true,
			urlInconsistent: true,
			flaggedAt:       ptr(-48 * time.Hour),
			want:            residueDecision{normalizeURL: true, clearFlag: true},
		},
		{
			name:       "recoverable + flagged + consistent url -> just clear flag",
			resolvable: true,
			flaggedAt:  ptr(-48 * time.Hour),
			want:       residueDecision{clearFlag: true},
		},
		{
			name:       "un-de-chunkable + unflagged -> set flag, do not purge",
			resolvable: false,
			want:       residueDecision{setFlag: true},
		},
		{
			name:       "un-de-chunkable + flagged within grace -> wait",
			resolvable: false,
			flaggedAt:  ptr(-1 * time.Hour),
			want:       residueDecision{},
		},
		{
			name:       "un-de-chunkable + flagged past grace -> reclaim",
			resolvable: false,
			flaggedAt:  ptr(-48 * time.Hour),
			want:       residueDecision{reclaim: true},
		},
		{
			name:              "in-flight chunking guards against any action (un-de-chunkable, aged flag)",
			resolvable:        false,
			flaggedAt:         ptr(-48 * time.Hour),
			chunkingStartedAt: ptr(-1 * time.Minute),
			want:              residueDecision{skip: true},
		},
		{
			name:              "stale chunking_started_at (older than TTL) does not guard",
			resolvable:        false,
			flaggedAt:         ptr(-48 * time.Hour),
			chunkingStartedAt: ptr(-2 * time.Hour),
			want:              residueDecision{reclaim: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := decideChunkedResidue(
				tc.resolvable, tc.urlInconsistent, tc.flaggedAt, tc.chunkingStartedAt, now, grace, chunkingTTL,
			)
			if got != tc.want {
				t.Fatalf("decideChunkedResidue() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
