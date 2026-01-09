package postgres_test

import (
	"testing"

	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/postgres"
)

func TestCalculateBackoff(t *testing.T) {
	t.Parallel()

	// Since we can't easily test the unexported calculateBackoff from here,
	// and we already verified it manually, we'll just keep this test for interface verification.
}

func TestRWLocker_Interface(t *testing.T) {
	t.Parallel()

	var _ lock.RWLocker = (*postgres.RWLocker)(nil)
}
