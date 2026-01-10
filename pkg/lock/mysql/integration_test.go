package mysql_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	driver "github.com/go-sql-driver/mysql"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/mysql"
)

// skipIfMysqlNotAvailable skips the test if MySQL is not available for testing.
func skipIfMysqlNotAvailable(t *testing.T) {
	t.Helper()

	if os.Getenv("NCPS_TEST_ADMIN_MYSQL_URL") == "" {
		t.Skip("MySQL tests disabled (set NCPS_TEST_ADMIN_MYSQL_URL to enable)")
	}
}

// getTestDatabase creates an ephemeral test database and returns a querier.
func getTestDatabase(t *testing.T) (database.Querier, func()) {
	t.Helper()

	adminDbURL := os.Getenv("NCPS_TEST_ADMIN_MYSQL_URL")
	require.NotEmpty(t, adminDbURL, "NCPS_TEST_ADMIN_MYSQL_URL must be set")

	// Connect to admin database
	adminDb, err := database.Open(adminDbURL, nil)
	require.NoError(t, err, "failed to connect to admin database")

	// Create ephemeral test database
	dbName := "test_lock_" + helper.MustRandString(20, nil)

	// In MySQL we use backticks for identifiers
	_, err = adminDb.DB().ExecContext(context.Background(), fmt.Sprintf("CREATE DATABASE `%s`", dbName))
	require.NoError(t, err, "failed to create test database")

	// Connect to test database
	testDbURL := replaceDatabaseName(t, adminDbURL, dbName)
	testDb, err := database.Open(testDbURL, nil)
	require.NoError(t, err, "failed to connect to test database")

	cleanup := func() {
		if err := testDb.DB().Close(); err != nil {
			t.Logf("error closing test database connection: %s", err)
		}

		_, err := adminDb.DB().ExecContext(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
		if err != nil {
			t.Logf("error dropping test database: %s", err)
		}

		if err := adminDb.DB().Close(); err != nil {
			t.Logf("error closing admin database connection: %s", err)
		}
	}

	return testDb, cleanup
}

// replaceDatabaseName replaces the database name in a MySQL DSN.
func replaceDatabaseName(t *testing.T, dbURL, newName string) string {
	t.Helper()

	// Parse using the driver if possible
	cfg, err := driver.ParseDSN(dbURL)
	if err == nil {
		cfg.DBName = newName

		return cfg.FormatDSN()
	}

	// Maybe it's a URL (mysql://)
	if strings.HasPrefix(dbURL, "mysql://") {
		u, err := url.Parse(dbURL)
		require.NoError(t, err)

		u.Path = "/" + newName

		return u.String()
	}

	// Fallback to manual string replacement (risky)
	// Assume the db name is the last part
	lastSlash := strings.LastIndex(dbURL, "/")
	if lastSlash != -1 {
		// handle query params
		questionMark := strings.Index(dbURL, "?")
		if questionMark != -1 {
			// keep params
			if questionMark > lastSlash {
				// remove old dbname, keep params
				return dbURL[:lastSlash+1] + newName + dbURL[questionMark:]
			}
			// if questionMark < lastSlash (e.g. part of host/protocol?), ignore query param logic for simpler cases
		}

		return dbURL[:lastSlash+1] + newName
	}

	require.Fail(t, "could not parse database URL/DSN")

	return ""
}

// getTestConfig returns a MySQL advisory lock configuration for testing.
func getTestConfig() mysql.Config {
	return mysql.Config{
		KeyPrefix: "test:ncps:lock:",
	}
}

// getTestRetryConfig returns a retry configuration for testing.
func getTestRetryConfig() lock.RetryConfig {
	return lock.RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     500 * time.Millisecond,
		Jitter:       true,
	}
}

// getUniqueKey generates a unique test key to avoid conflicts in parallel tests.
func getUniqueKey(t *testing.T, prefix string) string {
	t.Helper()

	return prefix + "-" + t.Name() + "-" + time.Now().Format("20060102-150405.000000")
}

func TestLocker_BasicLockUnlock(t *testing.T) {
	t.Parallel()
	skipIfMysqlNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	defer cleanup()

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker, err := mysql.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "basic-lock")

	// Acquire lock
	err = locker.Lock(ctx, key, 10*time.Second)
	require.NoError(t, err)

	// Release lock
	err = locker.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_ConcurrentLockContention(t *testing.T) {
	t.Parallel()
	skipIfMysqlNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	defer cleanup()

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	// Create two separate locker instances using same database
	locker1, err := mysql.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := mysql.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "contention")

	// First locker acquires lock
	err = locker1.Lock(ctx, key, 2*time.Second)
	require.NoError(t, err)

	// Second locker should fail to acquire (will retry and timeout)
	// Reduce max attempts for quicker test
	retryCfg2 := retryCfg
	retryCfg2.MaxAttempts = 2
	retryCfg2.MaxDelay = 100 * time.Millisecond

	locker2Fast, err := mysql.NewLocker(ctx, db, cfg, retryCfg2, false)
	require.NoError(t, err)

	ctx2, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	err = locker2Fast.Lock(ctx2, key, 2*time.Second)
	require.Error(t, err, "second locker should not acquire lock while first holds it")

	// Release first lock
	err = locker1.Unlock(ctx, key)
	require.NoError(t, err)

	// Now second locker can acquire
	err = locker2.Lock(ctx, key, 2*time.Second)
	require.NoError(t, err)

	err = locker2.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_TryLock(t *testing.T) {
	t.Parallel()
	skipIfMysqlNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	defer cleanup()

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker1, err := mysql.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := mysql.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "trylock")

	// First locker tries to acquire
	acquired, err := locker1.TryLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired, "first TryLock should succeed")

	// Second locker tries to acquire (should fail)
	acquired2, err := locker2.TryLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired2, "second TryLock should fail while first holds lock")

	// Release first lock
	err = locker1.Unlock(ctx, key)
	require.NoError(t, err)

	// Third TryLock should succeed
	acquired3, err := locker2.TryLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired3, "TryLock should succeed after lock released")

	// Cleanup
	err = locker2.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_RetryWithBackoff(t *testing.T) {
	t.Parallel()
	skipIfMysqlNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	defer cleanup()

	cfg := getTestConfig()
	retryCfg := lock.RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Jitter:       true,
	}

	locker1, err := mysql.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	locker2, err := mysql.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	key := getUniqueKey(t, "retry")

	// First locker acquires lock
	err = locker1.Lock(ctx, key, 3*time.Second)
	require.NoError(t, err)

	// Start goroutine to release lock after 1 second
	go func() {
		time.Sleep(1 * time.Second)

		_ = locker1.Unlock(ctx, key)
	}()

	// Second locker should retry and eventually acquire
	start := time.Now()
	err = locker2.Lock(ctx, key, 5*time.Second)
	duration := time.Since(start)

	require.NoError(t, err, "should eventually acquire lock after retries")
	assert.Greater(t, duration, 900*time.Millisecond, "should have waited for lock to be released")
	assert.Less(t, duration, 3*time.Second, "should not take too long with retries")

	err = locker2.Unlock(ctx, key)
	require.NoError(t, err)
}

func TestLocker_KeyHashing(t *testing.T) {
	t.Parallel()
	skipIfMysqlNotAvailable(t)

	ctx := context.Background()

	db, cleanup := getTestDatabase(t)
	defer cleanup()

	cfg := getTestConfig()
	retryCfg := getTestRetryConfig()

	locker, err := mysql.NewLocker(ctx, db, cfg, retryCfg, false)
	require.NoError(t, err)

	// Test that different keys get different locks
	key1 := getUniqueKey(t, "hash1")
	key2 := getUniqueKey(t, "hash2")

	err = locker.Lock(ctx, key1, 5*time.Second)
	require.NoError(t, err)

	// Should be able to acquire lock on different key
	err = locker.Lock(ctx, key2, 5*time.Second)
	require.NoError(t, err)

	err = locker.Unlock(ctx, key1)
	require.NoError(t, err)

	err = locker.Unlock(ctx, key2)
	require.NoError(t, err)
}
