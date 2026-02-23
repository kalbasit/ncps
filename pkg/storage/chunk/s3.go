package chunk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/s3"
	"github.com/kalbasit/ncps/pkg/zstd"
)

// ErrBucketNotFound is returned when the bucket is not found.
var ErrBucketNotFound = errors.New("bucket not found")

const (
	// s3NoSuchKey is the S3 error code for objects that don't exist.
	s3NoSuchKey = "NoSuchKey"
	// chunkPutLockTTL is the TTL for the lock acquired when putting a chunk.
	chunkPutLockTTL = 5 * time.Minute
)

// s3ReadCloser wraps a pooled zstd reader and io.ReadCloser to properly close both.
type s3ReadCloser struct {
	*zstd.PooledReader
	body io.ReadCloser
}

func (r *s3ReadCloser) Close() error {
	_ = r.PooledReader.Close()

	return r.body.Close()
}

// s3Store implements Store for S3 storage.
type s3Store struct {
	client *minio.Client
	locker lock.Locker
	bucket string
}

// NewS3Store returns a new S3 chunk store.
func NewS3Store(ctx context.Context, cfg s3.Config, locker lock.Locker) (Store, error) {
	if err := s3.ValidateConfig(cfg); err != nil {
		return nil, err
	}

	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid S3 endpoint: %w", err)
	}

	useSSL := u.Scheme == "https"
	endpoint := u.Host

	bucketLookup := minio.BucketLookupAuto
	if cfg.ForcePathStyle {
		bucketLookup = minio.BucketLookupPath
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       useSSL,
		Region:       cfg.Region,
		BucketLookup: bucketLookup,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating MinIO client: %w", err)
	}

	// Verify bucket exists
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("error checking bucket existence: %w", err)
	}

	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrBucketNotFound, cfg.Bucket)
	}

	return &s3Store{
		client: client,
		locker: locker,
		bucket: cfg.Bucket,
	}, nil
}

func (s *s3Store) HasChunk(ctx context.Context, hash string) (bool, error) {
	key, err := s.chunkPath(hash)
	if err != nil {
		return false, err
	}

	_, err = s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == s3NoSuchKey {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func (s *s3Store) GetChunk(ctx context.Context, hash string) (io.ReadCloser, error) {
	key, err := s.chunkPath(hash)
	if err != nil {
		return nil, err
	}

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == s3NoSuchKey {
			return nil, ErrNotFound
		}

		return nil, err
	}

	_, err = obj.Stat()
	if err != nil {
		obj.Close()

		if minio.ToErrorResponse(err).Code == s3NoSuchKey {
			return nil, ErrNotFound
		}

		return nil, err
	}

	// Use pooled reader instead of creating new instance
	pr, err := zstd.NewPooledReader(obj)
	if err != nil {
		obj.Close()

		return nil, fmt.Errorf("failed to create zstd reader: %w", err)
	}

	return &s3ReadCloser{pr, obj}, nil
}

func (s *s3Store) PutChunk(ctx context.Context, hash string, data []byte) (bool, int64, error) {
	key, err := s.chunkPath(hash)
	if err != nil {
		return false, 0, err
	}

	// Acquire a lock to prevent race conditions during check-then-act.
	// We use a prefix to avoid collisions with other locks.
	lockKey := fmt.Sprintf("chunk-put:%s", hash)
	if err := s.locker.Lock(ctx, lockKey, chunkPutLockTTL); err != nil {
		return false, 0, fmt.Errorf("error acquiring lock for chunk put: %w", err)
	}

	defer func() {
		_ = s.locker.Unlock(ctx, lockKey)
	}()

	// Use pooled encoder
	enc := zstd.GetWriter()
	defer zstd.PutWriter(enc)

	// Compress data with zstd
	compressed := enc.EncodeAll(data, nil)

	// Check if exists.
	exists, err := s.HasChunk(ctx, hash)
	if err != nil {
		return false, 0, err
	}

	if exists {
		return false, int64(len(compressed)), nil
	}

	_, err = s.client.PutObject(
		ctx,
		s.bucket,
		key,
		bytes.NewReader(compressed),
		int64(len(compressed)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"},
	)
	if err != nil {
		return false, 0, fmt.Errorf("error putting chunk to S3: %w", err)
	}

	return true, int64(len(compressed)), nil
}

func (s *s3Store) DeleteChunk(ctx context.Context, hash string) error {
	key, err := s.chunkPath(hash)
	if err != nil {
		return err
	}

	err = s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == s3NoSuchKey {
			return nil
		}

		return err
	}

	return nil
}

func (s *s3Store) WalkChunks(ctx context.Context, fn func(hash string) error) error {
	prefix := "store/chunk/"

	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	for object := range s.client.ListObjects(ctx, s.bucket, opts) {
		if object.Err != nil {
			return object.Err
		}

		hash := path.Base(object.Key)
		if len(hash) < 3 {
			continue
		}

		if err := fn(hash); err != nil {
			return err
		}
	}

	return nil
}

func (s *s3Store) chunkPath(hash string) (string, error) {
	if len(hash) < 3 {
		return "", fmt.Errorf("chunkPath hash=%q: %w", hash, helper.ErrInputTooShort)
	}

	return path.Join("store", "chunk", hash[:1], hash[:2], hash), nil
}
