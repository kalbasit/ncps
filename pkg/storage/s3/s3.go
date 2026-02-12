package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	narinfopkg "github.com/nix-community/go-nix/pkg/narinfo"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/narinfo"
	"github.com/kalbasit/ncps/pkg/s3"
	"github.com/kalbasit/ncps/pkg/storage"
)

const (
	otelPackageName = "github.com/kalbasit/ncps/pkg/storage/s3"

	// s3NoSuchKey is the S3 error code for objects that don't exist.
	s3NoSuchKey = "NoSuchKey"
)

var (
	// ErrInvalidConfig is returned if the S3 configuration is invalid.
	ErrInvalidConfig = errors.New("invalid S3 configuration")

	// ErrBucketNotFound is returned if the specified bucket does not exist.
	ErrBucketNotFound = errors.New("bucket not found")

	// ErrS3EndpointMissingScheme is returned if the S3 endpoint does not include a scheme.
	ErrS3EndpointMissingScheme = errors.New("S3 endpoint must include scheme (http:// or https://)")

	//nolint:gochecknoglobals
	tracer trace.Tracer
)

//nolint:gochecknoinits
func init() {
	tracer = otel.Tracer(otelPackageName)
}

// Store represents an S3 store and implements storage.Store.
type Store struct {
	client *minio.Client
	bucket string

	// secretKeyMu protects the secret key storage.
	secretKeyMu sync.Mutex
}

// New creates a new S3 store with the given configuration.
func New(ctx context.Context, cfg s3.Config) (*Store, error) {
	if err := s3.ValidateConfig(cfg); err != nil {
		return nil, err
	}

	// Parse SSL from endpoint scheme
	useSSL := s3.IsHTTPS(cfg.Endpoint)
	endpoint := s3.GetEndpointWithoutScheme(cfg.Endpoint)

	// Determine bucket lookup type based on ForcePathStyle
	bucketLookup := minio.BucketLookupAuto
	if cfg.ForcePathStyle {
		bucketLookup = minio.BucketLookupPath
	}

	// Create MinIO client
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       useSSL,
		Region:       cfg.Region,
		BucketLookup: bucketLookup,
		Transport:    cfg.Transport,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating MinIO client: %w", err)
	}

	// Test bucket access
	if err := testBucketAccess(ctx, client, cfg.Bucket); err != nil {
		return nil, fmt.Errorf("error testing bucket access: %w", err)
	}

	return &Store{
		client: client,
		bucket: cfg.Bucket,
	}, nil
}

// GetSecretKey returns secret key from the store.
func (s *Store) GetSecretKey(ctx context.Context) (signature.SecretKey, error) {
	key := s.secretKeyPath()

	_, span := tracer.Start(
		ctx,
		"s3.GetSecretKey",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("secret_key_path", key),
		),
	)
	defer span.End()

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return signature.SecretKey{}, fmt.Errorf("error getting secret key from S3: %w", err)
	}
	defer obj.Close()

	// Check for object existence before reading
	if _, err := obj.Stat(); err != nil {
		if minio.ToErrorResponse(err).Code == s3NoSuchKey {
			return signature.SecretKey{}, storage.ErrNotFound
		}

		return signature.SecretKey{}, fmt.Errorf("error getting secret key stat from S3: %w", err)
	}

	skc, err := io.ReadAll(obj)
	if err != nil {
		return signature.SecretKey{}, fmt.Errorf("error reading secret key: %w", err)
	}

	return signature.LoadSecretKey(string(skc))
}

// PutSecretKey stores the secret key in the store.
func (s *Store) PutSecretKey(ctx context.Context, sk signature.SecretKey) error {
	key := s.secretKeyPath()

	_, span := tracer.Start(
		ctx,
		"s3.PutSecretKey",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("secret_key_path", key),
		),
	)
	defer span.End()

	// RACE CONDITION FIX: Use a global mutex to serialize access to PutSecretKey.
	// This prevents the TOCTOU (time-of-check-time-of-use) race condition where
	// two concurrent calls could both see the key doesn't exist and both succeed.
	// This is sufficient for single-process deployments. For distributed deployments,
	// this should be replaced with a distributed lock.
	// https://github.com/kalbasit/ncps/pull/353#discussion_r2648008530
	s.secretKeyMu.Lock()
	defer s.secretKeyMu.Unlock()

	// Check if key already exists
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return storage.ErrAlreadyExists
	}

	errResp := minio.ToErrorResponse(err)
	if errResp.Code != s3NoSuchKey {
		return fmt.Errorf("error checking if secret key exists: %w", err)
	}

	// Put the secret key
	data := []byte(sk.String())

	_, err = s.client.PutObject(
		ctx,
		s.bucket,
		key,
		bytes.NewReader(data),
		int64(len(data)),
		minio.PutObjectOptions{ContentType: "text/plain"},
	)
	if err != nil {
		return fmt.Errorf("error putting secret key to S3: %w", err)
	}

	return nil
}

// DeleteSecretKey deletes the secret key in the store.
func (s *Store) DeleteSecretKey(ctx context.Context) error {
	key := s.secretKeyPath()

	_, span := tracer.Start(
		ctx,
		"s3.DeleteSecretKey",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("secret_key_path", key),
		),
	)
	defer span.End()

	// Check if key exists
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == s3NoSuchKey {
			return storage.ErrNotFound
		}

		return fmt.Errorf("error checking if secret key exists: %w", err)
	}

	err = s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("error deleting secret key from S3: %w", err)
	}

	return nil
}

// HasNarInfo returns true if the store has the narinfo.
func (s *Store) HasNarInfo(ctx context.Context, hash string) bool {
	key, err := s.narInfoPath(hash)
	if err != nil {
		return false
	}

	_, span := tracer.Start(
		ctx,
		"s3.HasNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_key", key),
		),
	)
	defer span.End()

	_, err = s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})

	return err == nil
}

// WalkNarInfos walks all narinfos in the store and calls fn for each one.
func (s *Store) WalkNarInfos(ctx context.Context, fn func(hash string) error) error {
	prefix := "store/narinfo/"

	_, span := tracer.Start(
		ctx,
		"s3.WalkNarInfos",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("prefix", prefix),
		),
	)
	defer span.End()

	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	for object := range s.client.ListObjects(ctx, s.bucket, opts) {
		if object.Err != nil {
			return object.Err
		}

		// key: store/narinfo/h/ha/hash.narinfo
		if !strings.HasSuffix(object.Key, ".narinfo") {
			continue
		}

		fileName := path.Base(object.Key)
		hash := strings.TrimSuffix(fileName, ".narinfo")

		if err := fn(hash); err != nil {
			return err
		}
	}

	return nil
}

// GetNarInfo returns narinfo from the store.
func (s *Store) GetNarInfo(ctx context.Context, hash string) (*narinfopkg.NarInfo, error) {
	key, err := s.narInfoPath(hash)
	if err != nil {
		return nil, err
	}

	_, span := tracer.Start(
		ctx,
		"s3.GetNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_key", key),
		),
	)
	defer span.End()

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("error getting narinfo from S3: %w", err)
	}
	defer obj.Close()

	// Try to stat the object to check if it exists
	_, err = obj.Stat()
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == s3NoSuchKey {
			return nil, storage.ErrNotFound
		}

		return nil, fmt.Errorf("error getting narinfo from S3: %w", err)
	}

	return narinfopkg.Parse(obj)
}

// PutNarInfo puts the narinfo in the store.
func (s *Store) PutNarInfo(ctx context.Context, hash string, narInfo *narinfopkg.NarInfo) error {
	key, err := s.narInfoPath(hash)
	if err != nil {
		return err
	}

	_, span := tracer.Start(
		ctx,
		"s3.PutNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_key", key),
		),
	)
	defer span.End()

	// Check if key already exists
	_, err = s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return storage.ErrAlreadyExists
	}

	errResp := minio.ToErrorResponse(err)
	if errResp.Code != s3NoSuchKey {
		return fmt.Errorf("error checking if narinfo exists: %w", err)
	}

	// Put the narinfo
	data := []byte(narInfo.String())

	_, err = s.client.PutObject(
		ctx,
		s.bucket,
		key,
		bytes.NewReader(data),
		int64(len(data)),
		minio.PutObjectOptions{ContentType: "text/x-nix-narinfo"},
	)
	if err != nil {
		return fmt.Errorf("error putting narinfo to S3: %w", err)
	}

	return nil
}

// DeleteNarInfo deletes the narinfo from the store.
func (s *Store) DeleteNarInfo(ctx context.Context, hash string) error {
	key, err := s.narInfoPath(hash)
	if err != nil {
		return err
	}

	_, span := tracer.Start(
		ctx,
		"s3.DeleteNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_key", key),
		),
	)
	defer span.End()

	// Check if key exists
	_, err = s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == s3NoSuchKey {
			return storage.ErrNotFound
		}

		return fmt.Errorf("error checking if narinfo exists: %w", err)
	}

	err = s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("error deleting narinfo from S3: %w", err)
	}

	return nil
}

// HasNar returns true if the store has the nar.
func (s *Store) HasNar(ctx context.Context, narURL nar.URL) bool {
	key, err := s.narPath(narURL)
	if err != nil {
		return false
	}

	_, span := tracer.Start(
		ctx,
		"s3.HasNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.String("nar_key", key),
		),
	)
	defer span.End()

	_, err = s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})

	return err == nil
}

// GetNar returns nar from the store.
// NOTE: The caller must close the returned io.ReadCloser!
func (s *Store) GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error) {
	key, err := s.narPath(narURL)
	if err != nil {
		return 0, nil, err
	}

	_, span := tracer.Start(
		ctx,
		"s3.GetNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.String("nar_key", key),
		),
	)
	defer span.End()

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return 0, nil, fmt.Errorf("error getting nar from S3: %w", err)
	}

	// Get object info for size
	info, err := obj.Stat()
	if err != nil {
		obj.Close()

		errResp := minio.ToErrorResponse(err)
		if errResp.Code == s3NoSuchKey {
			return 0, nil, storage.ErrNotFound
		}

		return 0, nil, fmt.Errorf("error getting nar info from S3: %w", err)
	}

	return info.Size, obj, nil
}

// PutNar puts the nar in the store.
func (s *Store) PutNar(ctx context.Context, narURL nar.URL, body io.Reader) (int64, error) {
	key, err := s.narPath(narURL)
	if err != nil {
		return 0, err
	}

	_, span := tracer.Start(
		ctx,
		"s3.PutNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.String("nar_key", key),
		),
	)
	defer span.End()

	// Check if key already exists
	_, err = s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return 0, storage.ErrAlreadyExists
	}

	errResp := minio.ToErrorResponse(err)
	if errResp.Code != s3NoSuchKey {
		return 0, fmt.Errorf("error checking if nar exists: %w", err)
	}

	// Determine content type based on compression
	contentType := "application/x-nix-nar"
	if ext := narURL.Compression.ToFileExtension(); ext != "" {
		contentType = "application/x-nix-nar-" + ext
	}

	// Put the nar - MinIO handles streaming uploads
	info, err := s.client.PutObject(
		ctx,
		s.bucket,
		key,
		body,
		-1, // unknown size, MinIO will handle it
		minio.PutObjectOptions{ContentType: contentType},
	)
	if err != nil {
		return 0, fmt.Errorf("error putting nar to S3: %w", err)
	}

	return info.Size, nil
}

// DeleteNar deletes the nar from the store.
func (s *Store) DeleteNar(ctx context.Context, narURL nar.URL) error {
	key, err := s.narPath(narURL)
	if err != nil {
		return err
	}

	_, span := tracer.Start(
		ctx,
		"s3.DeleteNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.String("nar_key", key),
		),
	)
	defer span.End()

	// Check if key exists
	_, err = s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == s3NoSuchKey {
			return storage.ErrNotFound
		}

		return fmt.Errorf("error checking if nar exists: %w", err)
	}

	err = s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("error deleting nar from S3: %w", err)
	}

	return nil
}

// Helper methods for key generation.
func (s *Store) secretKeyPath() string {
	return "config/cache.key"
}

func (s *Store) narInfoPath(hash string) (string, error) {
	nifP, err := narinfo.FilePath(hash)
	if err != nil {
		return "", err
	}

	return "store/narinfo/" + nifP, nil
}

func (s *Store) narPath(narURL nar.URL) (string, error) {
	normalizedURL, err := narURL.Normalize()
	if err != nil {
		return "", err
	}

	tfp, err := normalizedURL.ToFilePath()
	if err != nil {
		return "", err
	}

	return "store/nar/" + tfp, nil
}

func testBucketAccess(ctx context.Context, client *minio.Client, bucket string) error {
	log := zerolog.Ctx(ctx)

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		log.Error().Err(err).Str("bucket", bucket).Msg("error checking bucket existence")

		return fmt.Errorf("error checking bucket existence: %w", err)
	}

	if !exists {
		log.Error().Str("bucket", bucket).Msg("bucket does not exist")

		return fmt.Errorf("%w: %s", ErrBucketNotFound, bucket)
	}

	return nil
}
