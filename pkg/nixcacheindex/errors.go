package nixcacheindex

import "errors"

var (
	// ErrInvalidHashLength is returned when a hash string has an incorrect length.
	ErrInvalidHashLength = errors.New("invalid hash length")
	// ErrInvalidHashChar is returned when a hash string contains invalid characters.
	ErrInvalidHashChar = errors.New("invalid character in hash")
	// ErrInvalidMagic is returned when a shard file has an incorrect magic number.
	ErrInvalidMagic = errors.New("invalid magic number")
	// ErrEmptyShard is returned when trying to write a shard with no hashes.
	ErrEmptyShard = errors.New("cannot write empty shard")
	// ErrManifestNotFound is returned when the manifest cannot be fetched.
	ErrManifestNotFound = errors.New("manifest not found")
	// ErrShardNotFound is returned when a shard cannot be fetched.
	ErrShardNotFound = errors.New("shard not found")
	// ErrInvalidJournalOp is returned when a journal operation is invalid.
	ErrInvalidJournalOp = errors.New("invalid journal operation")
)
