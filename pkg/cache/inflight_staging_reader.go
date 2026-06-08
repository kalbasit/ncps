package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

// stagingReadPollInterval is how often a cross-pod reader re-reads staging_state
// while waiting for the next part-object to become available. It mirrors the
// progressive-chunk poll cadence.
const stagingReadPollInterval = 200 * time.Millisecond

var (
	// errStagingStall is returned when the producer stops advancing parts_available
	// and never publishes the complete marker within the per-part wait bound. It is
	// surfaced as a stream error so a truncated NAR is never delivered as a clean
	// EOF (the nar-concurrent-streaming correctness contract).
	errStagingStall = errors.New("staging reader: producer stalled before completion")

	// errStagingReset is returned when the staging_state row disappears mid-read
	// (a takeover reset it), so the reader fails rather than truncating.
	errStagingReset = errors.New("staging reader: staging_state was reset mid-stream")
)

// stagingServeInfo carries what a lock-losing waiter needs to serve a NAR from
// in-flight staging: the hash and the compression the parts hold.
type stagingServeInfo struct {
	hash        string
	compression nar.CompressionType
}

// stagingServeReady reports whether in-flight staging parts are available to serve
// for hash. It returns a non-nil directive once at least one part is readable and
// staging has not been abandoned; otherwise nil. A read error is treated as "not
// ready" so a transient DB hiccup never diverts the waiter into a broken serve.
func (c *Cache) stagingServeReady(ctx context.Context, hash string) *stagingServeInfo {
	st, err := c.getStagingState(ctx, hash)
	if err != nil || st == nil || st.PartsAvailable <= 0 {
		return nil
	}

	return &stagingServeInfo{
		hash:        hash,
		compression: nar.CompressionTypeFromString(st.Compression),
	}
}

// stagingPartReader reassembles a NAR from ordered staging part-objects, tailing
// them as the producer commits them. It is the cross-pod analogue of
// fileAvailableReader: it blocks until the next part is durably available
// (advertised via staging_state.parts_available), emits a clean io.EOF only once
// the terminal complete marker is set and every part has been consumed, and
// surfaces a stream error if the producer stalls or the staging state is reset.
type stagingPartReader struct {
	ctx  context.Context //nolint:containedctx // mirrors fileAvailableReader, a streaming reader
	c    *Cache
	hash string

	index int64         // next part index to open
	cur   io.ReadCloser // current part being drained, or nil

	pollEvery time.Duration // how often to re-check staging_state
	maxWait   time.Duration // per-part bound before declaring a stall
}

// newStagingPartReader builds a part-tailing reader for hash. The per-part stall
// bound reuses the progressive-chunk wait timeout for parity.
func (c *Cache) newStagingPartReader(ctx context.Context, hash string) *stagingPartReader {
	c.cdcMu.RLock()
	maxWait := c.chunkWaitTimeout
	c.cdcMu.RUnlock()

	if maxWait <= 0 {
		maxWait = defaultChunkWaitTimeout
	}

	return &stagingPartReader{
		ctx:       ctx,
		c:         c,
		hash:      hash,
		pollEvery: stagingReadPollInterval,
		maxWait:   maxWait,
	}
}

// Read implements io.Reader, draining each part-object in order and advancing to
// the next once the current one is exhausted.
func (r *stagingPartReader) Read(p []byte) (int, error) {
	// Fail fast if the caller's context is already done, before any part I/O.
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}

	for {
		if r.cur == nil {
			rc, err := r.openPart(r.index)
			if err != nil {
				return 0, err
			}

			r.cur = rc
		}

		n, err := r.cur.Read(p)
		if n > 0 {
			if errors.Is(err, io.EOF) {
				// Defer the EOF to the next call so trailing bytes are not dropped.
				r.advance()
			}

			return n, nil
		}

		if errors.Is(err, io.EOF) {
			r.advance()

			continue
		}

		if err != nil {
			return 0, err
		}
	}
}

// advance closes the current part and moves to the next index.
func (r *stagingPartReader) advance() {
	if r.cur != nil {
		_ = r.cur.Close()
		r.cur = nil
	}

	r.index++
}

// Close releases the in-flight part-object, if any.
func (r *stagingPartReader) Close() error {
	if r.cur != nil {
		err := r.cur.Close()
		r.cur = nil

		return err
	}

	return nil
}

// openPart blocks until part index is durably available and returns it, or
// returns io.EOF when staging is complete and every part has been consumed, or a
// stream error on stall / reset / context cancellation.
func (r *stagingPartReader) openPart(index int64) (io.ReadCloser, error) {
	ticker := time.NewTicker(r.pollEvery)
	defer ticker.Stop()

	// Per-part bound: reset implicitly per call, so a steadily-advancing producer
	// is never penalised while a stalled one is caught.
	timeout := time.NewTimer(r.maxWait)
	defer timeout.Stop()

	for {
		st, err := r.c.getStagingState(r.ctx, r.hash)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}

			return nil, fmt.Errorf("staging reader: read staging_state for %q: %w", r.hash, err)
		}

		if st == nil {
			return nil, fmt.Errorf("%w: %q", errStagingReset, r.hash)
		}

		switch {
		case index < st.PartsAvailable:
			rc, err := r.c.narStore.GetStagingPart(r.ctx, r.hash, index)
			if err == nil {
				return rc, nil
			}

			// A marker can momentarily lead the object's visibility; treat
			// not-found as transient and keep polling within the bound.
			if !errors.Is(err, storage.ErrNotFound) {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil, err
				}

				return nil, fmt.Errorf("staging reader: get part %d for %q: %w", index, r.hash, err)
			}
		case st.Status == stagingStatusComplete:
			// All parts are written and we have consumed them: clean EOF.
			return nil, io.EOF
		}

		select {
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		case <-timeout.C:
			return nil, fmt.Errorf("%w: part %d of %q after %s", errStagingStall, index, r.hash, r.maxWait)
		case <-ticker.C:
		}
	}
}

// stagingMultiReadCloser adapts a transcoding reader (e.g. a decompressor) while
// still closing the underlying part-tailing reader.
type stagingMultiReadCloser struct {
	io.Reader

	closers []io.Closer
}

func (m *stagingMultiReadCloser) Close() error {
	var firstErr error

	for _, c := range m.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// serveNarFromStaging builds a streaming reader that serves the NAR for hash from
// its in-flight staging part-objects. When the staged bytes are compressed but the
// client wants them uncompressed it transcodes on the fly, at parity with the
// same-pod streaming path, and rewrites narURL.Compression to the compression
// actually served. It returns size -1 because the length is not known up front.
func (c *Cache) serveNarFromStaging(
	ctx context.Context,
	narURL *nar.URL,
	hash string,
	staged nar.CompressionType,
) (int64, io.ReadCloser, error) {
	reader := c.newStagingPartReader(ctx, hash)

	if !isNoneCompression(staged) && isNoneCompression(narURL.Compression) {
		dr, err := nar.DecompressReader(ctx, reader, staged)
		if err != nil {
			_ = reader.Close()

			return 0, nil, fmt.Errorf("staging serve: decompress %s for %q: %w", staged, hash, err)
		}

		narURL.Compression = nar.CompressionTypeNone

		return -1, &stagingMultiReadCloser{Reader: dr, closers: []io.Closer{dr, reader}}, nil
	}

	// Serve the staged bytes as-is and advertise their compression.
	narURL.Compression = staged

	return -1, reader, nil
}

// isNoneCompression reports whether ct represents uncompressed bytes. Both the
// explicit "none" type and the empty string (the staging_state default) count.
func isNoneCompression(ct nar.CompressionType) bool {
	return ct == nar.CompressionTypeNone || ct == nar.CompressionType("")
}
