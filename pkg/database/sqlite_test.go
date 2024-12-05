package database_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/pkg/database"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestOpen(t *testing.T) {
	t.Run("database does not exist yet", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "database-path-")
		if err != nil {
			t.Fatalf("expected no error, got: %q", err)
		}
		defer os.RemoveAll(dir) // clean up

		dbpath := filepath.Join(dir, "db.sqlite")

		t.Run("database does not exist yet", func(t *testing.T) {
			_, err := os.Stat(dbpath)
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		db, err := database.Open(logger, dbpath)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		t.Run("database does exist now", func(t *testing.T) {
			_, err := os.Stat(dbpath)
			if err != nil {
				t.Fatalf("expected no error but got: %s", err)
			}
		})

		t.Run("database has the narinfos table", func(t *testing.T) {
			rows, err := db.Query("SELECT name FROM sqlite_master WHERE type=? AND name=?", "table", "narinfos")
			if err != nil {
				t.Fatalf("error inserting a narinfo: %s", err)
			}

			defer rows.Close()

			names := make([]string, 0)
			for rows.Next() {
				var name string

				if err := rows.Scan(&name); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				names = append(names, name)
			}

			if want, got := 1, len(names); want != got {
				t.Fatalf("want %d got %d", want, got)
			}

			if want, got := "narinfos", names[0]; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("database has the nars table", func(t *testing.T) {
			rows, err := db.Query("SELECT name FROM sqlite_master WHERE type=? AND name=?", "table", "nars")
			if err != nil {
				t.Fatalf("error inserting a narinfo: %s", err)
			}

			defer rows.Close()

			names := make([]string, 0)
			for rows.Next() {
				var name string

				if err := rows.Scan(&name); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				names = append(names, name)
			}

			if want, got := 1, len(names); want != got {
				t.Fatalf("want %d got %d", want, got)
			}

			if want, got := "nars", names[0]; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})
	})
}
