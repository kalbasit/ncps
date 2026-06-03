package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage"
)

// TestNarInfoErrorStatus verifies that the narinfo GET handler maps a leaked
// errNarInfoPurged sentinel to HTTP 404 (never HTTP 500), as defense in depth,
// alongside the existing storage.ErrNotFound and context-cancellation handling.
func TestNarInfoErrorStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantRespond bool
	}{
		{"not found maps to 404", storage.ErrNotFound, http.StatusNotFound, true},
		{"purge sentinel maps to 404", cache.ErrNarInfoPurged, http.StatusNotFound, true},
		{
			"wrapped purge sentinel maps to 404",
			fmt.Errorf("getting narinfo: %w", cache.ErrNarInfoPurged),
			http.StatusNotFound,
			true,
		},
		{"context canceled writes nothing", context.Canceled, 0, false},
		{"deadline exceeded writes nothing", context.DeadlineExceeded, 0, false},
		{"unknown error maps to 500", io.ErrUnexpectedEOF, http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, respond := server.NarInfoErrorStatus(tt.err)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantRespond, respond)
		})
	}
}
