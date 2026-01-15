package nixcacheindex

import (
	"encoding/json"
	"io"
	"os"
	"time"
)

// ManifestPath is the well-known path for the cache index manifest.
const ManifestPath = "/nix-cache-index/manifest.json"

// Manifest describes the index topology.
type Manifest struct {
	Version   int       `json:"version"`
	Format    string    `json:"format"`
	CreatedAt time.Time `json:"created_at"` //nolint:tagliatelle // RFC 0195
	ItemCount int64     `json:"item_count"` //nolint:tagliatelle // RFC 0195
	Sharding  Sharding  `json:"sharding"`
	Encoding  Encoding  `json:"encoding"`
	Urls      Urls      `json:"urls"`
	Journal   Journal   `json:"journal"`
	Epoch     Epoch     `json:"epoch"`
	Deltas    Deltas    `json:"deltas"`
}

// Sharding configuration.
type Sharding struct {
	Depth    int    `json:"depth"`
	Alphabet string `json:"alphabet"`
}

// Encoding configuration for shards.
type Encoding struct {
	Type       string `json:"type"`        // e.g. "golomb-rice"
	Parameter  int    `json:"parameter"`   // Golomb parameter k (M = 2^k)
	HashBits   int    `json:"hash_bits"`   //nolint:tagliatelle // RFC 0195
	PrefixBits int    `json:"prefix_bits"` //nolint:tagliatelle // RFC 0195
}

// Urls configuration.
type Urls struct {
	JournalBase string `json:"journal_base"` //nolint:tagliatelle // RFC 0195
	ShardsBase  string `json:"shards_base"`  //nolint:tagliatelle // RFC 0195
	DeltasBase  string `json:"deltas_base"`  //nolint:tagliatelle // RFC 0195
}

// Journal configuration.
type Journal struct {
	CurrentSegment         int64 `json:"current_segment"`          //nolint:tagliatelle // RFC 0195
	SegmentDurationSeconds int   `json:"segment_duration_seconds"` //nolint:tagliatelle // RFC 0195
	RetentionCount         int   `json:"retention_count"`          //nolint:tagliatelle // RFC 0195
}

// Epoch information.
type Epoch struct {
	Current  int64 `json:"current"`
	Previous int64 `json:"previous,omitempty"`
}

// Deltas configuration.
type Deltas struct {
	Enabled     bool   `json:"enabled"`
	OldestBase  int64  `json:"oldest_base"` //nolint:tagliatelle // RFC 0195
	Compression string `json:"compression"` // "none", "gzip", "zstd"
}

// NewManifest creates a default manifest.
func NewManifest() *Manifest {
	return &Manifest{
		Version:   1,
		Format:    "hlssi",
		CreatedAt: time.Now().UTC(),
		Sharding: Sharding{
			Depth:    2,
			Alphabet: Alphabet,
		},
		Encoding: Encoding{
			Type:       "golomb-rice",
			Parameter:  8,
			HashBits:   HashBits,
			PrefixBits: 10,
		},
		Urls: Urls{
			JournalBase: "https://cache.example.com/nix-cache-index/journal/",
			ShardsBase:  "https://cache.example.com/nix-cache-index/shards/",
			DeltasBase:  "https://cache.example.com/nix-cache-index/deltas/",
		},
		Journal: Journal{
			CurrentSegment:         time.Now().Unix(),
			SegmentDurationSeconds: 300,
			RetentionCount:         12,
		},
		Epoch: Epoch{
			Current: 1,
		},
		Deltas: Deltas{
			Enabled:     true,
			Compression: "zstd",
		},
	}
}

// LoadManifest reads a manifest from an io.Reader.
func LoadManifest(r io.Reader) (*Manifest, error) {
	var m Manifest
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return nil, err
	}

	return &m, nil
}

// LoadManifestFromFile reads a manifest from a file.
func LoadManifestFromFile(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return LoadManifest(f)
}

// WriteManifest writes the manifest to an io.Writer.
func (m *Manifest) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(m)
}
