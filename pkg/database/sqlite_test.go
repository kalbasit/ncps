package database_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/mattn/go-sqlite3"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestOpen(t *testing.T) {
	t.Run("database does not exist yet", func(t *testing.T) {
		t.Parallel()

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
				t.Fatalf("error querying sqlite_master: %s", err)
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

func TestInsertNarInfoRecord(t *testing.T) {
	dir, err := os.MkdirTemp("", "database-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	dbpath := filepath.Join(dir, "db.sqlite")

	db, err := database.Open(logger, dbpath)
	if err != nil {
		t.Fatalf("expected no error but got: %s", err)
	}

	t.Run("inserting one record", func(t *testing.T) {
		t.Parallel()

		hash, err := helper.RandString(32, nil)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		//nolint:errcheck
		defer tx.Rollback()

		res, err := db.InsertNarInfoRecord(tx, hash)
		if err != nil {
			t.Fatalf("expected no error got: %s", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("expected no error got: %s", err)
		}

		rows, err := db.Query("SELECT id, hash, created_at, updated_at, last_accessed_at FROM narinfos")
		if err != nil {
			t.Fatalf("error selecting narinfos: %s", err)
		}

		defer rows.Close()

		nims := make([]database.NarInfoModel, 0)

		for rows.Next() {
			var nim database.NarInfoModel

			if err := rows.Scan(&nim.ID, &nim.Hash, &nim.CreatedAt, &nim.UpdatedAt, &nim.LastAccessedAt); err != nil {
				t.Fatalf("expected no error got: %s", err)
			}

			nims = append(nims, nim)
		}

		if want, got := 1, len(nims); want != got {
			t.Fatalf("want %d got %d", want, got)
		}

		lid, err := res.LastInsertId()
		if err != nil {
			t.Errorf("error getting the last access id: %s", err)
		}

		if want, got := lid, nims[0].ID; want != got {
			t.Errorf("want %d got %d", want, got)
		}

		if want, got := hash, nims[0].Hash; want != got {
			t.Errorf("want %s got %s", want, got)
		}

		old := time.Since(nims[0].CreatedAt)
		if old > 3*time.Second {
			t.Errorf("expected the nim to have a created at less than 3s got: %s", old)
		}

		if nims[0].UpdatedAt != nil {
			t.Errorf("expected no updated_at field, found: %s", nims[0].UpdatedAt)
		}

		if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; !reflect.DeepEqual(want, got) {
			t.Errorf("want %s got %s", want, got)
		}
	})

	t.Run("hash is unique", func(t *testing.T) {
		t.Parallel()

		hash, err := helper.RandString(32, nil)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		//nolint:errcheck
		defer tx.Rollback()

		if _, err := db.InsertNarInfoRecord(tx, hash); err != nil {
			t.Fatalf("expected no error got: %s", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("expected no error got: %s", err)
		}

		tx, err = db.Begin()
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		//nolint:errcheck
		defer tx.Rollback()

		_, err = db.InsertNarInfoRecord(tx, hash)

		sqliteErr, ok := errors.Unwrap(err).(sqlite3.Error)
		if !ok {
			t.Fatalf("error should be castable to sqliteErr but it was not: %s", err)
		}

		if want, got := sqlite3.ErrConstraint, sqliteErr.Code; want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})
}

func TestTouchNarInfoRecord(t *testing.T) {
	dir, err := os.MkdirTemp("", "database-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	dbpath := filepath.Join(dir, "db.sqlite")

	db, err := database.Open(logger, dbpath)
	if err != nil {
		t.Fatalf("expected no error but got: %s", err)
	}

	t.Run("no narinfo existing", func(t *testing.T) {
		t.Parallel()

		// create a narinfo
		hash, err := helper.RandString(32, nil)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("error beginning a transaction: %s", err)
		}

		//nolint:errcheck
		defer tx.Rollback()

		err = db.TouchNarInfoRecord(tx, hash)

		sqliteErr, ok := errors.Unwrap(err).(sqlite3.Error)
		if !ok {
			t.Fatalf("error should be castable to sqliteErr but it was not: %s", err)
		}

		if want, got := sqlite3.ErrNotFound, sqliteErr.Code; want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})
}

//nolint:paralleltest
func TestInsertNarRecord(t *testing.T) {
	dir, err := os.MkdirTemp("", "database-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	dbpath := filepath.Join(dir, "db.sqlite")

	db, err := database.Open(logger, dbpath)
	if err != nil {
		t.Fatalf("expected no error but got: %s", err)
	}

	// create a narinfo
	hash, err := helper.RandString(32, nil)
	if err != nil {
		t.Fatalf("expected no error but got: %s", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("expected no error but got: %s", err)
	}

	//nolint:errcheck
	defer tx.Rollback()

	res, err := db.InsertNarInfoRecord(tx, hash)
	if err != nil {
		t.Fatalf("expected no error got: %s", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("expected no error got: %s", err)
	}

	nid, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("expected no error got: %s", err)
	}

	for _, compression := range []string{"", "xz", "tar.gz"} {
		t.Run(fmt.Sprintf("compression=%q", compression), func(t *testing.T) {
			if _, err := db.Exec("DELETE FROM nars"); err != nil {
				t.Fatalf("error removing all existing nar records: %s", err)
			}

			t.Run("inserting one record", func(t *testing.T) {
				hash, err := helper.RandString(32, nil)
				if err != nil {
					t.Fatalf("expected no error but got: %s", err)
				}

				tx, err := db.Begin()
				if err != nil {
					t.Fatalf("expected no error but got: %s", err)
				}

				//nolint:errcheck
				defer tx.Rollback()

				res, err := db.InsertNarRecord(tx, nid, hash, compression, 123)
				if err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				if err := tx.Commit(); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				const query = `
				SELECT id, narinfo_id, hash, compression, file_size, created_at, updated_at, last_accessed_at
				FROM nars
				`

				rows, err := db.Query(query)
				if err != nil {
					t.Fatalf("error selecting narinfos: %s", err)
				}

				defer rows.Close()

				nims := make([]database.NarModel, 0)

				for rows.Next() {
					var nim database.NarModel

					err := rows.Scan(
						&nim.ID,
						&nim.NarInfoID,
						&nim.Hash,
						&nim.Compression,
						&nim.FileSize,
						&nim.CreatedAt,
						&nim.UpdatedAt,
						&nim.LastAccessedAt,
					)
					if err != nil {
						t.Fatalf("expected no error got: %s", err)
					}

					nims = append(nims, nim)
				}

				if want, got := 1, len(nims); want != got {
					t.Fatalf("want %d got %d", want, got)
				}

				lid, err := res.LastInsertId()
				if err != nil {
					t.Errorf("error getting the last access id: %s", err)
				}

				if want, got := lid, nims[0].ID; want != got {
					t.Errorf("want %d got %d", want, got)
				}

				if want, got := nid, nims[0].NarInfoID; want != got {
					t.Errorf("want %d got %d", want, got)
				}

				if want, got := hash, nims[0].Hash; want != got {
					t.Errorf("want %s got %s", want, got)
				}

				if want, got := compression, nims[0].Compression; want != got {
					t.Errorf("want %s got %s", want, got)
				}

				if want, got := uint64(123), nims[0].FileSize; want != got {
					t.Errorf("want %d got %d", want, got)
				}

				old := time.Since(nims[0].CreatedAt)
				if old > 3*time.Second {
					t.Errorf("expected the nim to have a created at less than 3s got: %s", old)
				}

				if nims[0].UpdatedAt != nil {
					t.Errorf("expected no updated_at field, found: %s", nims[0].UpdatedAt)
				}

				if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; !reflect.DeepEqual(want, got) {
					t.Errorf("want %s got %s", want, got)
				}
			})

			t.Run("hash is unique", func(t *testing.T) {
				hash, err := helper.RandString(32, nil)
				if err != nil {
					t.Fatalf("expected no error but got: %s", err)
				}

				tx, err := db.Begin()
				if err != nil {
					t.Fatalf("expected no error but got: %s", err)
				}

				//nolint:errcheck
				defer tx.Rollback()

				if _, err := db.InsertNarRecord(tx, nid, hash, "", 123); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				if err := tx.Commit(); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				tx, err = db.Begin()
				if err != nil {
					t.Fatalf("expected no error but got: %s", err)
				}

				//nolint:errcheck
				defer tx.Rollback()

				_, err = db.InsertNarRecord(tx, nid, hash, "", 123)

				sqliteErr, ok := errors.Unwrap(err).(sqlite3.Error)
				if !ok {
					t.Fatalf("error should be castable to sqliteErr but it was not: %s", err)
				}

				if want, got := sqlite3.ErrConstraint, sqliteErr.Code; want != got {
					t.Errorf("want %q got %q", want, got)
				}
			})
		})
	}
}
