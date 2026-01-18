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
	StorePath      sql.NullString
	URL            sql.NullString
	Compression    sql.NullString
	FileHash       sql.NullString
	FileSize       sql.NullInt64
	NarHash        sql.NullString
	NarSize        sql.NullInt64
	Deriver        sql.NullString
	System         sql.NullString
	Ca             sql.NullString
}

// CreateNarFileParams holds parameters for creating a NAR file entry.
type CreateNarFileParams struct {
	Hash        string
	Compression string
	Query       string
	FileSize    uint64
}

// CreateNarInfoParams holds parameters for creating a NarInfo entry.
type CreateNarInfoParams struct {
	Hash        string
	StorePath   sql.NullString
	URL         sql.NullString
	Compression sql.NullString
	FileHash    sql.NullString
	FileSize    sql.NullInt64
	NarHash     sql.NullString
	NarSize     sql.NullInt64
	Deriver     sql.NullString
	System      sql.NullString
	Ca          sql.NullString
}

// AddNarInfoReferenceParams holds parameters for adding a reference to a NarInfo.
type AddNarInfoReferenceParams struct {
	NarInfoID int64
	Reference string
}

// AddNarInfoSignatureParams holds parameters for adding a signature to a NarInfo.
type AddNarInfoSignatureParams struct {
	NarInfoID int64
	Signature string
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
