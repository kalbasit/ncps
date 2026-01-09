package postgres

// Config holds the configuration for PostgreSQL advisory locks.
type Config struct {
	// KeyPrefix is prepended to all lock keys for namespacing.
	// Defaults to "ncps:lock:" if empty.
	KeyPrefix string
}
