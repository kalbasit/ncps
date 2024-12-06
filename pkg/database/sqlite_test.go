package database_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inconshreveable/log15/v3"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

//nolint:paralleltest
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

			if err := rows.Err(); err != nil {
				t.Fatalf("got an error on rows: %s", err)
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

			if err := rows.Err(); err != nil {
				t.Fatalf("got an error on rows: %s", err)
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

//nolint:paralleltest
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

		if err := rows.Err(); err != nil {
			t.Fatalf("got an error on rows: %s", err)
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

		if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
			t.Errorf("expected created_at == last_accessed_at got: %q == %q", want, got)
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

		if want, got := database.ErrAlreadyExists, err; !errors.Is(got, want) {
			t.Errorf("want %q got %q", want, got)
		}
	})

	t.Run("can write many narinfos", func(t *testing.T) {
		var wg sync.WaitGroup

		errC := make(chan error)

		for i := 0; i < 10000; i++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				hash, err := helper.RandString(128, nil)
				if err != nil {
					errC <- fmt.Errorf("expected no error but got: %w", err)

					return
				}

				tx, err := db.Begin()
				if err != nil {
					errC <- fmt.Errorf("expected no error but got: %w", err)

					return
				}

				//nolint:errcheck
				defer tx.Rollback()

				if _, err := db.InsertNarInfoRecord(tx, hash); err != nil {
					errC <- fmt.Errorf("expected no error got: %w", err)

					return
				}

				if err := tx.Commit(); err != nil {
					errC <- fmt.Errorf("expected no error got: %w", err)

					return
				}
			}()
		}

		done := make(chan struct{})

		go func() {
			wg.Wait()

			close(done)
		}()

		for {
			select {
			case err := <-errC:
				t.Errorf("got an error: %s", err)
			case <-done:
				return
			}
		}
	})
}

//nolint:paralleltest
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

	t.Run("narinfo not existing", func(t *testing.T) {
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

		res, err := db.TouchNarInfoRecord(tx, hash)
		if err != nil {
			t.Fatalf("error touching a narinfo record: %s", err)
		}

		ra, err := res.RowsAffected()
		if err != nil {
			t.Fatalf("error getting rows affected: %s", err)
		}

		if want, got := int64(0), ra; want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})

	t.Run("narinfo existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		t.Run("create the narinfo", func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			//nolint:errcheck
			defer tx.Rollback()

			if _, err := db.InsertNarInfoRecord(tx, hash); err != nil {
				t.Fatalf("error inserting the record: %s", err)
			}

			if err := tx.Commit(); err != nil {
				t.Fatalf("error committing transaction: %s", err)
			}
		})

		t.Run("confirm created_at == last_accessed_at, and no updated_at", func(t *testing.T) {
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

			if err := rows.Err(); err != nil {
				t.Fatalf("got an error on rows: %s", err)
			}

			if want, got := 1, len(nims); want != got {
				t.Fatalf("want %d got %d", want, got)
			}

			if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
				t.Errorf("expected created_at == last_accessed_at got: %q == %q", want, got)
			}

			if ua := nims[0].UpdatedAt; ua != nil {
				t.Errorf("expected updated_at to be nil got: %s", ua)
			}
		})

		t.Run("touch the narinfo", func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			//nolint:errcheck
			defer tx.Rollback()

			time.Sleep(time.Second)

			res, err := db.TouchNarInfoRecord(tx, hash)
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			if err := tx.Commit(); err != nil {
				t.Fatalf("error committing transaction: %s", err)
			}

			ra, err := res.RowsAffected()
			if err != nil {
				t.Fatalf("error getting rows affected: %s", err)
			}

			if want, got := int64(1), ra; want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("confirm created_at != last_accessed_at and updated_at == last_accessed_at", func(t *testing.T) {
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

			if err := rows.Err(); err != nil {
				t.Fatalf("got an error on rows: %s", err)
			}

			if want, got := 1, len(nims); want != got {
				t.Fatalf("want %d got %d", want, got)
			}

			if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() == got.Unix() {
				t.Errorf("expected created_at != last_accessed_at got: %q == %q", want, got)
			}

			if want, got := nims[0].UpdatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
				t.Errorf("expected updated_at == last_accessed_at got: %q == %q", want, got)
			}
		})
	})
}

//nolint:paralleltest
func TestDeleteNarInfoRecord(t *testing.T) {
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

	t.Run("narinfo not existing", func(t *testing.T) {
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

		if err := db.DeleteNarInfoRecord(tx, hash); err != nil {
			t.Errorf("error deleting narinfo record: %s", err)
		}
	})

	t.Run("narinfo existing", func(t *testing.T) {
		hash, err := helper.RandString(32, nil)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		t.Run("create the narinfo", func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			//nolint:errcheck
			defer tx.Rollback()

			if _, err := db.InsertNarInfoRecord(tx, hash); err != nil {
				t.Fatalf("error inserting the record: %s", err)
			}

			if err := tx.Commit(); err != nil {
				t.Fatalf("error committing transaction: %s", err)
			}
		})

		t.Run("delete the narinfo", func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			//nolint:errcheck
			defer tx.Rollback()

			time.Sleep(time.Second)

			if err := db.DeleteNarInfoRecord(tx, hash); err != nil {
				t.Fatalf("error deleting a narinfo record: %s", err)
			}

			if err := tx.Commit(); err != nil {
				t.Fatalf("error committing transaction: %s", err)
			}
		})

		t.Run("confirm it has been removed", func(t *testing.T) {
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

			if err := rows.Err(); err != nil {
				t.Fatalf("got an error on rows: %s", err)
			}

			if want, got := 0, len(nims); want != got {
				t.Fatalf("want %d got %d", want, got)
			}
		})
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

				if err := rows.Err(); err != nil {
					t.Fatalf("got an error on rows: %s", err)
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

				if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
					t.Errorf("expected created_at == last_accessed_at got: %q == %q", want, got)
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

				if want, got := database.ErrAlreadyExists, err; !errors.Is(got, want) {
					t.Errorf("want %q got %q", want, got)
				}
			})
		})
	}
}

//nolint:paralleltest
func TestTouchNarRecord(t *testing.T) {
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

	t.Run("nar not existing", func(t *testing.T) {
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

		res, err := db.TouchNarRecord(tx, hash)
		if err != nil {
			t.Fatalf("error beginning a transaction: %s", err)
		}

		ra, err := res.RowsAffected()
		if err != nil {
			t.Fatalf("error beginning a transaction: %s", err)
		}

		if want, got := int64(0), ra; want != got {
			t.Fatalf("want %d got %d", want, got)
		}
	})

	t.Run("nar existing", func(t *testing.T) {
		var nid int64

		t.Run("create the narinfo", func(t *testing.T) {
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

			nid, err = res.LastInsertId()
			if err != nil {
				t.Fatalf("expected no error got: %s", err)
			}
		})

		hash, err := helper.RandString(32, nil)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		t.Run("create the nar", func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			//nolint:errcheck
			defer tx.Rollback()

			if _, err := db.InsertNarRecord(tx, nid, hash, "", 123); err != nil {
				t.Fatalf("error inserting the record: %s", err)
			}

			if err := tx.Commit(); err != nil {
				t.Fatalf("error committing transaction: %s", err)
			}
		})

		t.Run("confirm created_at == last_accessed_at, and no updated_at", func(t *testing.T) {
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

			if err := rows.Err(); err != nil {
				t.Fatalf("got an error on rows: %s", err)
			}

			if want, got := 1, len(nims); want != got {
				t.Fatalf("want %d got %d", want, got)
			}

			if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
				t.Errorf("expected created_at == last_accessed_at got: %q == %q", want, got)
			}

			if ua := nims[0].UpdatedAt; ua != nil {
				t.Errorf("expected updated_at to be nil got: %s", ua)
			}
		})

		t.Run("touch the nar", func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			//nolint:errcheck
			defer tx.Rollback()

			time.Sleep(time.Second)

			res, err := db.TouchNarRecord(tx, hash)
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			if err := tx.Commit(); err != nil {
				t.Fatalf("error committing transaction: %s", err)
			}

			ra, err := res.RowsAffected()
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			if want, got := int64(1), ra; want != got {
				t.Fatalf("want %d got %d", want, got)
			}
		})

		t.Run("confirm created_at != last_accessed_at and updated_at == last_accessed_at", func(t *testing.T) {
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

			if err := rows.Err(); err != nil {
				t.Fatalf("got an error on rows: %s", err)
			}

			if want, got := 1, len(nims); want != got {
				t.Fatalf("want %d got %d", want, got)
			}

			if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() == got.Unix() {
				t.Errorf("expected created_at != last_accessed_at got: %q == %q", want, got)
			}

			if want, got := nims[0].UpdatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
				t.Errorf("expected updated_at == last_accessed_at got: %q == %q", want, got)
			}
		})
	})
}

//nolint:paralleltest
func TestDeleteNarRecord(t *testing.T) {
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

	t.Run("nar not existing", func(t *testing.T) {
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

		if err := db.DeleteNarRecord(tx, hash); err != nil {
			t.Errorf("error deleting narinfo record: %s", err)
		}
	})

	t.Run("nar existing", func(t *testing.T) {
		var nid int64

		t.Run("create the narinfo", func(t *testing.T) {
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

			nid, err = res.LastInsertId()
			if err != nil {
				t.Fatalf("expected no error got: %s", err)
			}
		})

		hash, err := helper.RandString(32, nil)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}

		t.Run("create the nar", func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			//nolint:errcheck
			defer tx.Rollback()

			if _, err := db.InsertNarRecord(tx, nid, hash, "", 123); err != nil {
				t.Fatalf("error inserting the record: %s", err)
			}

			if err := tx.Commit(); err != nil {
				t.Fatalf("error committing transaction: %s", err)
			}
		})

		t.Run("delete the narinfo", func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("error beginning a transaction: %s", err)
			}

			//nolint:errcheck
			defer tx.Rollback()

			time.Sleep(time.Second)

			if err := db.DeleteNarRecord(tx, hash); err != nil {
				t.Fatalf("error deleting a narinfo record: %s", err)
			}

			if err := tx.Commit(); err != nil {
				t.Fatalf("error committing transaction: %s", err)
			}
		})

		t.Run("confirm it has been removed", func(t *testing.T) {
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

			if err := rows.Err(); err != nil {
				t.Fatalf("got an error on rows: %s", err)
			}

			if want, got := 0, len(nims); want != got {
				t.Fatalf("want %d got %d", want, got)
			}
		})
	})
}
