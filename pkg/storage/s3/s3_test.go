package s3

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
)

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
				Bucket:          "test-bucket",
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			},
			wantErr: false,
		},
		{
			name: "missing bucket",
			config: Config{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			},
			wantErr: true,
		},
		{
			name: "missing access key",
			config: Config{
				Bucket:          "test-bucket",
				SecretAccessKey: "test-secret",
			},
			wantErr: true,
		},
		{
			name: "missing secret key",
			config: Config{
				Bucket:      "test-bucket",
				AccessKeyID: "test-key",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateConfig(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestKeyGeneration(t *testing.T) {
	t.Parallel()

	store := &Store{bucket: "test-bucket"}

	t.Run("secret key path", func(t *testing.T) {
		t.Parallel()

		path := store.secretKeyPath()
		assert.Equal(t, "config/cache.key", path)
	})

	t.Run("narinfo path", func(t *testing.T) {
		t.Parallel()

		path := store.narInfoPath("abc123")
		assert.Equal(t, "store/narinfo/a/ab/abc123.narinfo", path)
	})

	t.Run("nar path", func(t *testing.T) {
		t.Parallel()

		narURL := nar.URL{
			Hash:        "abc123",
			Compression: nar.CompressionTypeNone,
		}
		path := store.narPath(narURL)
		assert.Equal(t, "store/nar/a/ab/abc123.nar", path)
	})

	t.Run("nar path with compression", func(t *testing.T) {
		t.Parallel()

		narURL := nar.URL{
			Hash:        "abc123",
			Compression: nar.CompressionTypeBzip2,
		}
		path := store.narPath(narURL)
		assert.Equal(t, "store/nar/a/ab/abc123.nar.bz2", path)
	})
}

func TestCreateAWSConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("basic config", func(t *testing.T) {
		t.Parallel()

		cfg := Config{
			Bucket:          "test-bucket",
			AccessKeyID:     "test-key",
			SecretAccessKey: "test-secret",
		}

		awsCfg, err := createAWSConfig(ctx, cfg)
		require.NoError(t, err)
		assert.NotNil(t, awsCfg)
	})

	t.Run("with region", func(t *testing.T) {
		t.Parallel()

		cfg := Config{
			Bucket:          "test-bucket",
			Region:          "us-west-2",
			AccessKeyID:     "test-key",
			SecretAccessKey: "test-secret",
		}

		awsCfg, err := createAWSConfig(ctx, cfg)
		require.NoError(t, err)
		assert.NotNil(t, awsCfg)
	})

	t.Run("with endpoint", func(t *testing.T) {
		t.Parallel()

		cfg := Config{
			Bucket:          "test-bucket",
			Endpoint:        "http://localhost:9000",
			AccessKeyID:     "test-key",
			SecretAccessKey: "test-secret",
		}

		awsCfg, err := createAWSConfig(ctx, cfg)
		require.NoError(t, err)
		assert.NotNil(t, awsCfg)
	})
}

// Mock S3 client for testing.
type mockS3Client struct {
	objects map[string][]byte
	exists  map[string]bool
}

func newMockS3Client() *mockS3Client {
	return &mockS3Client{
		objects: make(map[string][]byte),
		exists:  make(map[string]bool),
	}
}

func (m *mockS3Client) GetObject(
	_ context.Context,
	params *s3.GetObjectInput,
	_ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	key := *params.Key
	if data, exists := m.objects[key]; exists {
		contentLength := int64(len(data))

		return &s3.GetObjectOutput{
			Body:          io.NopCloser(strings.NewReader(string(data))),
			ContentLength: &contentLength,
		}, nil
	}

	return nil, &types.NoSuchKey{}
}

func (m *mockS3Client) PutObject(
	_ context.Context,
	params *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	key := *params.Key
	data, err := io.ReadAll(params.Body)
	if err != nil {
		return nil, err
	}
	m.objects[key] = data
	m.exists[key] = true

	return &s3.PutObjectOutput{}, nil
}

func (m *mockS3Client) HeadObject(
	_ context.Context,
	params *s3.HeadObjectInput,
	_ ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	key := *params.Key
	if m.exists[key] {
		return &s3.HeadObjectOutput{}, nil
	}

	return nil, &types.NoSuchKey{}
}

func (m *mockS3Client) DeleteObject(
	_ context.Context,
	params *s3.DeleteObjectInput,
	_ ...func(*s3.Options),
) (*s3.DeleteObjectOutput, error) {
	key := *params.Key
	if m.exists[key] {
		delete(m.objects, key)
		delete(m.exists, key)

		return &s3.DeleteObjectOutput{}, nil
	}

	return nil, &types.NoSuchKey{}
}

func (m *mockS3Client) HeadBucket(
	context.Context,
	*s3.HeadBucketInput,
	...func(*s3.Options),
) (*s3.HeadBucketOutput, error) {
	return &s3.HeadBucketOutput{}, nil
}

// Test store with mock client.
func TestStoreWithMock(t *testing.T) {
	t.Parallel()

	mockClient := newMockS3Client()
	store := &Store{
		client: mockClient,
		bucket: "test-bucket",
	}

	t.Run("basic operations", func(t *testing.T) {
		t.Parallel()

		// Test that the store can be created
		assert.NotNil(t, store)
		assert.Equal(t, "test-bucket", store.bucket)
	})

	t.Run("key generation", func(t *testing.T) {
		t.Parallel()

		// Test key generation methods
		assert.Equal(t, "config/cache.key", store.secretKeyPath())
		assert.Equal(t, "store/narinfo/a/ab/abc123.narinfo", store.narInfoPath("abc123"))

		narURL := nar.URL{
			Hash:        "abc123",
			Compression: nar.CompressionTypeNone,
		}
		assert.Equal(t, "store/nar/a/ab/abc123.nar", store.narPath(narURL))
	})
}
