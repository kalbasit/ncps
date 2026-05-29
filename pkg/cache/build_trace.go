package cache

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	entbuildtraceentry "github.com/kalbasit/ncps/ent/buildtraceentry"
	entbuildtracesignature "github.com/kalbasit/ncps/ent/buildtracesignature"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/storage"
)

// buildTraceKey is the key portion of a build-trace-v2 entry.
type buildTraceKey struct {
	DrvPath    string `json:"drvPath"`
	OutputName string `json:"outputName"`
}

// buildTraceSig is the v2 signature format used by build-trace-v2.
type buildTraceSig struct {
	KeyName string `json:"keyName"`
	Sig     string `json:"sig"`
}

// buildTraceValue is the value portion of a build-trace-v2 entry.
type buildTraceValue struct {
	OutPath    string          `json:"outPath"`
	Signatures []buildTraceSig `json:"signatures,omitempty"`
}

// buildTraceEntryJSON is the full build-trace-v2 entry (v3 schema).
type buildTraceEntryJSON struct {
	Key   buildTraceKey   `json:"key"`
	Value buildTraceValue `json:"value"`
}

// buildTraceFingerprint computes the fingerprint used for signing: the full
// JSON entry with value.signatures removed, matching the nix reference
// implementation in realisation.cc.
func buildTraceFingerprint(e buildTraceEntryJSON) (string, error) {
	e.Value.Signatures = nil

	b, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("marshal fingerprint: %w", err)
	}

	return string(b), nil
}

// drvFullPath reconstructs the absolute store path from the basename used in
// the URL. Nix derivations are always under /nix/store/.
func drvFullPath(drvName string) string {
	return "/nix/store/" + drvName
}

// HasBuildTrace returns true if a build trace entry for (drvName, outputName)
// exists in the database.
func (c *Cache) HasBuildTrace(ctx context.Context, drvName, outputName string) bool {
	exists, err := c.dbClient.Ent().BuildTraceEntry.Query().
		Where(
			entbuildtraceentry.DrvPathEQ(drvFullPath(drvName)),
			entbuildtraceentry.OutputNameEQ(outputName),
		).
		Exist(ctx)
	if err != nil {
		return false
	}

	return exists
}

// GetBuildTrace retrieves a stored build trace entry and returns its JSON
// representation. The response is reconstructed from structured DB columns
// and all stored signatures so that ncps's own signature is always present.
func (c *Cache) GetBuildTrace(ctx context.Context, drvName, outputName string) ([]byte, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.GetBuildTrace",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("drv_name", drvName),
			attribute.String("output_name", outputName),
		),
	)
	defer span.End()

	bte, err := c.dbClient.Ent().BuildTraceEntry.Query().
		Where(
			entbuildtraceentry.DrvPathEQ(drvFullPath(drvName)),
			entbuildtraceentry.OutputNameEQ(outputName),
		).
		WithSignatures().
		Only(ctx)
	if err != nil {
		if database.IsNotFoundError(err) {
			return nil, storage.ErrNotFound
		}

		return nil, fmt.Errorf("query build trace entry: %w", err)
	}

	sigs := make([]buildTraceSig, 0, len(bte.Edges.Signatures))
	for _, s := range bte.Edges.Signatures {
		sigs = append(sigs, buildTraceSig{
			KeyName: s.KeyName,
			Sig:     s.Signature,
		})
	}

	entry := buildTraceEntryJSON{
		Key: buildTraceKey{
			DrvPath:    bte.DrvPath,
			OutputName: bte.OutputName,
		},
		Value: buildTraceValue{
			OutPath:    bte.OutPath,
			Signatures: sigs,
		},
	}

	b, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal build trace entry: %w", err)
	}

	return b, nil
}

// PutBuildTrace stores a build trace entry from an uploaded JSON body.
// It parses the body, validates that the URL params match the body key,
// appends ncps's own signature, then upserts into the database.
func (c *Cache) PutBuildTrace(ctx context.Context, drvName, outputName string, r io.Reader) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.PutBuildTrace",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("drv_name", drvName),
			attribute.String("output_name", outputName),
		),
	)
	defer span.End()

	raw, err := io.ReadAll(io.LimitReader(r, 1024*1024))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	var entry buildTraceEntryJSON
	if err := json.Unmarshal(raw, &entry); err != nil {
		return fmt.Errorf("%w: %w", ErrBadRequest, err)
	}

	if entry.Key.DrvPath == "" || entry.Key.OutputName == "" || entry.Value.OutPath == "" {
		return fmt.Errorf("%w: missing required fields in build trace entry", ErrBadRequest)
	}

	// Validate that the body key matches the full expected store path.
	expectedDrvPath := drvFullPath(drvName)
	if entry.Key.DrvPath != expectedDrvPath {
		return fmt.Errorf("%w: expected drvPath %q, got %q",
			ErrBadRequest, expectedDrvPath, entry.Key.DrvPath)
	}

	if entry.Key.OutputName != outputName {
		return fmt.Errorf("%w: URL outputName %q does not match body %q",
			ErrBadRequest, outputName, entry.Key.OutputName)
	}

	if err := c.signBuildTrace(ctx, &entry); err != nil {
		return fmt.Errorf("sign build trace: %w", err)
	}

	return c.withEntTransaction(ctx, "PutBuildTrace", func(tx *ent.Tx) error {
		// Upsert the entry row, then re-query to get the ID for signature insertion.
		if err := tx.BuildTraceEntry.Create().
			SetDrvPath(entry.Key.DrvPath).
			SetOutputName(outputName).
			SetOutPath(entry.Value.OutPath).
			SetRawJSON(string(raw)).
			OnConflictColumns(entbuildtraceentry.FieldDrvPath, entbuildtraceentry.FieldOutputName).
			UpdateOutPath().
			UpdateRawJSON().
			Exec(ctx); err != nil {
			return fmt.Errorf("upsert build trace entry: %w", err)
		}

		bte, err := tx.BuildTraceEntry.Query().
			Where(
				entbuildtraceentry.DrvPathEQ(drvFullPath(drvName)),
				entbuildtraceentry.OutputNameEQ(outputName),
			).
			Only(ctx)
		if err != nil {
			return fmt.Errorf("query upserted build trace entry: %w", err)
		}

		// Replace signatures: delete existing, then bulk-insert new ones.
		if _, err := tx.BuildTraceSignature.Delete().
			Where(entbuildtracesignature.BuildTraceEntryIDEQ(bte.ID)).
			Exec(ctx); err != nil {
			return fmt.Errorf("delete old build trace signatures: %w", err)
		}

		return addBuildTraceSignatures(ctx, tx, bte.ID, entry.Value.Signatures)
	})
}

// signBuildTrace appends ncps's own signature to the build trace entry,
// matching the pattern of signNarInfo. The fingerprint is the JSON
// representation of the entry with value.signatures removed.
func (c *Cache) signBuildTrace(ctx context.Context, entry *buildTraceEntryJSON) error {
	_, span := tracer.Start(
		ctx,
		"cache.signBuildTrace",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	defer span.End()

	fp, err := buildTraceFingerprint(*entry)
	if err != nil {
		return fmt.Errorf("fingerprint: %w", err)
	}

	sig, err := c.secretKey.Sign(nil, fp)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	// Remove any existing signature from ncps's own key, then append the new one.
	sigs := make([]buildTraceSig, 0, len(entry.Value.Signatures)+1)
	for _, s := range entry.Value.Signatures {
		if s.KeyName != c.hostName {
			sigs = append(sigs, s)
		}
	}

	sigs = append(sigs, buildTraceSig{
		KeyName: sig.Name,
		Sig:     base64.StdEncoding.EncodeToString(sig.Data),
	})

	entry.Value.Signatures = sigs

	return nil
}

// addBuildTraceSignatures bulk-inserts signature rows for a build trace entry.
func addBuildTraceSignatures(
	ctx context.Context,
	tx *ent.Tx,
	entryID int,
	sigs []buildTraceSig,
) error {
	if len(sigs) == 0 {
		return nil
	}

	bulk := make([]*ent.BuildTraceSignatureCreate, len(sigs))
	for i, s := range sigs {
		bulk[i] = tx.BuildTraceSignature.Create().
			SetBuildTraceEntryID(entryID).
			SetKeyName(s.KeyName).
			SetSignature(s.Sig)
	}

	if err := tx.BuildTraceSignature.CreateBulk(bulk...).
		OnConflictColumns(entbuildtracesignature.FieldBuildTraceEntryID, entbuildtracesignature.FieldKeyName).
		UpdateSignature().
		Exec(ctx); err != nil {
		return fmt.Errorf("insert build trace signatures: %w", err)
	}

	return nil
}

// ErrBadRequest is returned when the client sends an invalid build trace body.
var ErrBadRequest = errors.New("bad request")
