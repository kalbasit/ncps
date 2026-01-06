package database

import (
	"database/sql"
	"time"
)

type CreateConfigParams struct {
	Key   string
	Value string
}

type Config struct {
	ID        int64
	Key       string
	Value     string
	CreatedAt time.Time
	UpdatedAt sql.NullTime
}

// NarFile represents a cached NAR file.
type NarFile struct {
	ID             int64
	Hash           string
	Compression    string
	FileSize       uint64
	Query          string
	CreatedAt      time.Time
	UpdatedAt      sql.NullTime
	LastAccessedAt sql.NullTime
}

// NarInfo represents metadata about a Nix store path.
type NarInfo struct {
	ID             int64
	Hash           string
	CreatedAt      time.Time
	UpdatedAt      sql.NullTime
	LastAccessedAt sql.NullTime
}

// CreateNarFileParams holds parameters for creating a NAR file entry.
type CreateNarFileParams struct {
	Hash        string
	Compression string
	Query       string
	FileSize    uint64
}

// LinkNarInfoToNarFileParams holds parameters for linking a NarInfo to a NarFile.
type LinkNarInfoToNarFileParams struct {
	NarInfoID int64
	NarFileID int64
}

type SetConfigParams struct {
	Key   string
	Value string
}
