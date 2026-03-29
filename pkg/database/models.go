package database

import (
	"database/sql"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// NarInfo represents the narinfos table.
type NarInfo struct {
	bun.BaseModel `bun:"table:narinfos"`

	ID             int64           `bun:"id,pk,autoincrement"`
	Hash           string          `bun:"hash,notnull,unique"`
	CreatedAt      time.Time       `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt      schema.NullTime `bun:"updated_at"`
	LastAccessedAt schema.NullTime `bun:"last_accessed_at"`
	StorePath      sql.NullString  `bun:"store_path"`
	URL            sql.NullString  `bun:"url"`
	Compression    sql.NullString  `bun:"compression"`
	FileHash       sql.NullString  `bun:"file_hash"`
	FileSize       sql.NullInt64   `bun:"file_size"`
	NarHash        sql.NullString  `bun:"nar_hash"`
	NarSize        sql.NullInt64   `bun:"nar_size"`
	Deriver        sql.NullString  `bun:"deriver"`
	System         sql.NullString  `bun:"system"`
	Ca             sql.NullString  `bun:"ca"`
}

// NarFile represents the nar_files table.
type NarFile struct {
	bun.BaseModel `bun:"table:nar_files"`

	ID                int64           `bun:"id,pk,autoincrement"`
	Hash              string          `bun:"hash,notnull"`
	Compression       string          `bun:"compression,notnull,default:''"`
	FileSize          uint64          `bun:"file_size,notnull"`
	Query             string          `bun:"query,notnull,default:''"`
	CreatedAt         time.Time       `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt         schema.NullTime `bun:"updated_at"`
	LastAccessedAt    schema.NullTime `bun:"last_accessed_at"`
	TotalChunks       int64           `bun:"total_chunks"`
	ChunkingStartedAt schema.NullTime `bun:"chunking_started_at"`
	VerifiedAt        schema.NullTime `bun:"verified_at"`

	// Unique constraint on (hash, compression, query)
	_ struct{} `bun:"unique:hash_compression_query"`
}

// Chunk represents the chunks table.
type Chunk struct {
	bun.BaseModel `bun:"table:chunks"`

	ID             int64           `bun:"id,pk,autoincrement"`
	Hash           string          `bun:"hash,notnull,unique"`
	Size           uint32          `bun:"size,notnull"`
	CompressedSize uint32          `bun:"compressed_size,notnull,default:0"`
	CreatedAt      time.Time       `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt      schema.NullTime `bun:"updated_at"`
}

// Config represents the config table.
type Config struct {
	bun.BaseModel `bun:"table:config"`

	ID        int64           `bun:"id,pk,autoincrement"`
	Key       string          `bun:"key,notnull,unique"`
	Value     string          `bun:"value,notnull"`
	CreatedAt time.Time       `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt schema.NullTime `bun:"updated_at"`
}

// PinnedClosure represents the pinned_closures table.
type PinnedClosure struct {
	bun.BaseModel `bun:"table:pinned_closures"`

	ID        int64           `bun:"id,pk,autoincrement"`
	Hash      string          `bun:"hash,notnull,unique"`
	CreatedAt time.Time       `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt schema.NullTime `bun:"updated_at"`
}

// NarInfoReference represents the narinfo_references junction table.
type NarInfoReference struct {
	bun.BaseModel `bun:"table:narinfo_references"`

	NarInfoID int64  `bun:"narinfo_id,notnull"`
	Reference string `bun:"reference,notnull"`

	_ struct{} `bun:"pk: narinfo_id, reference"`
}

// NarInfoSignature represents the narinfo_signatures junction table.
type NarInfoSignature struct {
	bun.BaseModel `bun:"table:narinfo_signatures"`

	NarInfoID int64  `bun:"narinfo_id,notnull"`
	Signature string `bun:"signature,notnull"`

	_ struct{} `bun:"pk: narinfo_id, signature"`
}

// NarFileChunk represents the nar_file_chunks junction table.
type NarFileChunk struct {
	bun.BaseModel `bun:"table:nar_file_chunks"`

	NarFileID  int64 `bun:"nar_file_id,notnull"`
	ChunkID    int64 `bun:"chunk_id,notnull"`
	ChunkIndex int64 `bun:"chunk_index,notnull"`

	_ struct{} `bun:"pk: nar_file_id, chunk_index"`
}

// NarInfoNarFile represents the narinfo_nar_files junction table.
type NarInfoNarFile struct {
	bun.BaseModel `bun:"table:narinfo_nar_files"`

	NarInfoID int64 `bun:"narinfo_id,notnull"`
	NarFileID int64 `bun:"nar_file_id,notnull"`

	_ struct{} `bun:"pk: narinfo_id, nar_file_id"`
}

// NarFilePinnedClosure represents a junction table if needed for pinned closures.
// NOTE: Based on current schema, pinned_closures is a standalone table with unique hash.
// If a junction is needed in future, add it here.
type NarFilePinnedClosure struct {
	bun.BaseModel `bun:"table:nar_file_pinned_closures"`

	NarFileID       int64 `bun:"nar_file_id,notnull"`
	PinnedClosureID int64 `bun:"pinned_closure_id,notnull"`

	_ struct{} `bun:"pk: nar_file_id, pinned_closure_id"`
}
