package s3_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	s3config "github.com/kalbasit/ncps/pkg/s3"
	storage_s3 "github.com/kalbasit/ncps/pkg/storage/s3"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

const cacheName = "cache.example.com"

const (
	s3LocationResponseString = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`

	s3NoSuchKey = "NoSuchKey"
)

var (
	errConnectionFailed    = errors.New("connection failed")
	errListObjectsFailed   = errors.New("list objects failed")
	errPutFailed           = errors.New("put failed")
	errGetFailed           = errors.New("get failed")
	errDeleteFailed        = errors.New("delete failed")
	errTestingBucketAccess = errors.New("error testing bucket access")
	errCallbackFailed      = errors.New("callback error")

	//nolint:gochecknoglobals
	s3ListObjectsResponse = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
    <Name>test-bucket</Name>
    <Prefix>store/narinfo/</Prefix>
    <Contents>
        <Key>store/narinfo/valid.narinfo</Key>
        <LastModified>2023-10-25T00:00:00.000Z</LastModified>
        <ETag>&quot;d41d8cd98f00b204e9800998ecf8427e&quot;</ETag>
        <Size>0</Size>
        <Owner>
            <ID>minioadmin</ID>
            <DisplayName>minioadmin</DisplayName>
        </Owner>
        <StorageClass>STANDARD</StorageClass>
    </Contents>
    <Contents>
        <Key>store/narinfo/other.file</Key>
         <LastModified>2023-10-25T00:00:00.000Z</LastModified>
        <ETag>&quot;d41d8cd98f00b204e9800998ecf8427e&quot;</ETag>
        <Size>0</Size>
         <Owner>
            <ID>minioadmin</ID>
            <DisplayName>minioadmin</DisplayName>
        </Owner>
        <StorageClass>STANDARD</StorageClass>
    </Contents>
</ListBucketResult>`
)

func TestSecretKey_ErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("GetSecretKey Read error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errGetFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "config/cache.key") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		_, err = store.GetSecretKey(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error reading secret key")
	})

	t.Run("GetSecretKey Stat error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errGetFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// First GET request succeeds but body is read later
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "config/cache.key") {
				return s3OKResponse("some-key")
			}

			// HEAD request (Stat) fails
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "config/cache.key") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		_, err = store.GetSecretKey(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error getting secret key stat from S3")
	})

	t.Run("PutSecretKey StatObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errConnectionFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) fails with unexpected error
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "config/cache.key") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		sk, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		err = store.PutSecretKey(ctx, sk)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error checking if secret key exists")
	})

	t.Run("PutSecretKey PutObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errPutFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) returns 404 (Not Found), allowing Put to proceed
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "config/cache.key") {
				return s3NotFoundResponse(s3NoSuchKey, "The specified key does not exist.")
			}

			// PutObject fails
			if req.Method == http.MethodPut && strings.Contains(req.URL.Path, "config/cache.key") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		sk, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		err = store.PutSecretKey(ctx, sk)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error putting secret key to S3")
	})

	t.Run("DeleteSecretKey StatObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errConnectionFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) fails with unexpected error
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "config/cache.key") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		err = store.DeleteSecretKey(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error checking if secret key exists")
	})

	t.Run("DeleteSecretKey RemoveObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errDeleteFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) succeeds
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "config/cache.key") {
				return s3OKResponse("")
			}

			// RemoveObject fails
			if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "config/cache.key") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		err = store.DeleteSecretKey(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error deleting secret key from S3")
	})
}

func getTestStore(t *testing.T) *storage_s3.Store {
	t.Helper()

	cfg := getTestConfig(t)
	if cfg == nil {
		return nil
	}

	ctx := newContext()

	store, err := storage_s3.New(ctx, *cfg)
	require.NoError(t, err)

	return store
}

func getTestConfig(t *testing.T) *s3config.Config {
	return testhelper.S3TestConfig(t)
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("missing bucket returns error", func(t *testing.T) {
		t.Parallel()

		cfg := s3config.Config{
			Endpoint:        "http://localhost:9000",
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
		}

		_, err := storage_s3.New(newContext(), cfg)
		assert.ErrorIs(t, err, s3config.ErrBucketRequired)
	})

	t.Run("missing endpoint returns error", func(t *testing.T) {
		t.Parallel()

		cfg := s3config.Config{
			Bucket:          "test-bucket",
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
		}

		_, err := storage_s3.New(newContext(), cfg)
		assert.ErrorIs(t, err, s3config.ErrEndpointRequired)
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func s3NotFoundResponse(code, message string) (*http.Response, error) {
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>%s</Code>
  <Message>%s</Message>
  <RequestId>4442587FB7D0A2F9</RequestId>
  <HostId>nmIPG6bRoc0OcSR89Our983D5z77Fv9A=</HostId>
</Error>`, code, message)

	header := make(http.Header)
	header.Set("Content-Type", "application/xml")
	header.Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")

	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func s3OKResponse(body string) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestBucketAccess_ErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("BucketExists returns error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errConnectionFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, expectedErr
		})

		_, err := storage_s3.New(ctx, cfgWithMock)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection failed")
	})

	t.Run("bucket does not exist", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			// BucketExists/GetBucketLocation check
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3NotFoundResponse("NoSuchBucket", "The specified bucket does not exist.")
			}
			// Fallback for HEAD
			if req.Method == http.MethodHead && strings.HasSuffix(req.URL.Path, "test-bucket") {
				return s3NotFoundResponse("NoSuchBucket", "The specified bucket does not exist.")
			}

			return s3OKResponse("")
		})

		_, err := storage_s3.New(ctx, cfgWithMock)
		assert.ErrorIs(t, err, storage_s3.ErrBucketNotFound)
	})
}

func TestWalkNarInfos_ErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("ListObjects returns error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errListObjectsFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			// BucketExists check in New
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// Then it lists objects
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "test-bucket") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		err = store.WalkNarInfos(ctx, func(_ string) error {
			return nil
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list objects failed")
	})
}

func TestWalkNarInfos_Structure(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("WalkNarInfos handles valid and invalid keys and callback errors", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			// BucketExists check in New
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// ListObjects
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3ListObjectsResponse)
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		t.Run("Filters non-narinfo keys", func(t *testing.T) {
			t.Parallel()

			var visited []string

			err := store.WalkNarInfos(ctx, func(hash string) error {
				visited = append(visited, hash)

				return nil
			})
			require.NoError(t, err)
			require.Len(t, visited, 1)
			assert.Equal(t, "valid", visited[0])
		})

		t.Run("Propagates callback error", func(t *testing.T) {
			t.Parallel()

			err := store.WalkNarInfos(ctx, func(_ string) error {
				return errCallbackFailed
			})
			require.ErrorIs(t, err, errCallbackFailed)
		})
	})
}

func TestHasNarInfo_ErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("HasNarInfo StatObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errConnectionFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) fails with unexpected error
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, ".narinfo") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		exists := store.HasNarInfo(ctx, "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc27")
		assert.False(t, exists)
	})

	t.Run("HasNarInfo invalid hash returns false", func(t *testing.T) {
		t.Parallel()

		// No transport needed as checking happens before network call
		// But New requires it for BucketExists
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		exists := store.HasNarInfo(ctx, "in") // invalid hash (too short)
		assert.False(t, exists)
	})
}

func TestDeleteNarInfo_ErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("DeleteNarInfo StatObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errConnectionFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) fails with unexpected error
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, ".narinfo") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		err = store.DeleteNarInfo(ctx, "0a90gw9sdyz3680wfncd5xf0qg6zh27w")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error checking if narinfo exists")
	})

	t.Run("DeleteNarInfo RemoveObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errDeleteFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) succeeds
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, ".narinfo") {
				return s3OKResponse("")
			}

			// RemoveObject fails
			if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, ".narinfo") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		err = store.DeleteNarInfo(ctx, "0a90gw9sdyz3680wfncd5xf0qg6zh27w")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error deleting narinfo from S3")
	})

	t.Run("DeleteNarInfo invalid hash returns error", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		err = store.DeleteNarInfo(ctx, "in") // invalid hash
		require.Error(t, err)
	})
}

func TestNarInfo_ErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("GetNarInfo results in 404", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, ".narinfo") {
				return s3NotFoundResponse("NoSuchKey", "The specified key does not exist.")
			}

			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, ".narinfo") {
				return s3NotFoundResponse("NoSuchKey", "The specified key does not exist.")
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		_, err = store.GetNarInfo(ctx, "0a90gw9sdyz3680wfncd5xf0qg6zh27w")
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("PutNarInfo returns error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errPutFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			if req.Method == http.MethodPut && strings.Contains(req.URL.Path, ".narinfo") {
				return nil, expectedErr
			}

			// StatObject (to check if it exists)
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, ".narinfo") {
				return s3NotFoundResponse("NoSuchKey", "The specified key does not exist.")
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		err = store.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, ni)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "put failed")
	})

	t.Run("GetNarInfo with invalid hash returns error", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		_, err = store.GetNarInfo(ctx, "in") // invalid hash
		require.Error(t, err)
	})

	t.Run("PutNarInfo with invalid hash returns error", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		ni := &narinfo.NarInfo{}
		err = store.PutNarInfo(ctx, "in", ni) // invalid hash
		require.Error(t, err)
	})
}

func TestHasNar_ErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("HasNar StatObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errConnectionFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) fails with unexpected error
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "store/nar") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc27", Compression: "none"}
		exists := store.HasNar(ctx, narURL)
		assert.False(t, exists)
	})

	t.Run("HasNar invalid URL returns false", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "in", Compression: "none"} // invalid hash
		exists := store.HasNar(ctx, narURL)
		assert.False(t, exists)
	})
}

func TestPutNar_ErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("PutNar StatObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errConnectionFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) fails with unexpected error
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "store/nar") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc27", Compression: "none"}
		_, err = store.PutNar(ctx, narURL, strings.NewReader("content"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error checking if nar exists")
	})

	t.Run("PutNar PutObject error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errPutFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) returns 404 (Not Found), allowing Put to proceed
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "store/nar") {
				return s3NotFoundResponse(s3NoSuchKey, "The specified key does not exist.")
			}

			// PutObject fails
			if req.Method == http.MethodPut && strings.Contains(req.URL.Path, "store/nar") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc27", Compression: "none"}
		_, err = store.PutNar(ctx, narURL, strings.NewReader("content"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error putting nar to S3")
	})

	t.Run("PutNar with invalid URL returns error", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "in", Compression: "none"} // invalid hash
		_, err = store.PutNar(ctx, narURL, strings.NewReader("content"))
		require.Error(t, err)
	})
}

func TestNar_ErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := newContext()
	cfg := s3config.Config{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	}

	t.Run("GetNar returns error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errGetFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "store/nar") {
				return nil, expectedErr
			}

			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "store/nar") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc27", Compression: "none"}
		_, _, err = store.GetNar(ctx, narURL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get failed")
	})

	t.Run("DeleteNar StatObject returns error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errConnectionFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			// StatObject (HEAD) fails with unexpected error
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "store/nar") {
				return nil, expectedErr
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc27", Compression: "none"}
		err = store.DeleteNar(ctx, narURL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error checking if nar exists")
	})

	t.Run("DeleteNar returns error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errDeleteFailed
		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "store/nar") {
				return nil, expectedErr
			}

			// StatObject (to check if it exists)
			if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "store/nar") {
				return s3OKResponse("")
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc27", Compression: "none"}
		err = store.DeleteNar(ctx, narURL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
	})

	t.Run("GetNar with invalid URL returns error", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "in", Compression: "none"} // invalid hash
		_, _, err = store.GetNar(ctx, narURL)
		require.Error(t, err)
	})

	t.Run("DeleteNar with invalid URL returns error", func(t *testing.T) {
		t.Parallel()

		cfgWithMock := cfg
		cfgWithMock.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Has("location") && strings.Contains(req.URL.Path, "test-bucket") {
				return s3OKResponse(s3LocationResponseString)
			}

			return s3OKResponse("")
		})

		store, err := storage_s3.New(ctx, cfgWithMock)
		require.NoError(t, err)

		narURL := nar.URL{Hash: "in", Compression: "none"} // invalid hash
		err = store.DeleteNar(ctx, narURL)
		require.Error(t, err)
	})
}

// Integration tests - require running MinIO instance

//nolint:paralleltest
func TestGetSecretKey_Integration(t *testing.T) {
	// Note: Secret key tests cannot run in parallel as they share the same path
	t.Run("no secret key is present", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		// Make sure it doesn't exist first
		_ = store.DeleteSecretKey(ctx)

		_, err := store.GetSecretKey(ctx)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})
}

//nolint:paralleltest
func TestPutSecretKey_Integration(t *testing.T) {
	// Note: Secret key tests cannot run in parallel as they share the same path
	t.Run("put secret key successfully", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		sk, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		// Clean up first
		_ = store.DeleteSecretKey(ctx)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteSecretKey(ctx)
		})

		require.NoError(t, store.PutSecretKey(ctx, sk))

		// Verify it was stored
		sk2, err := store.GetSecretKey(ctx)
		require.NoError(t, err)

		assert.Equal(t, sk.String(), sk2.String())
	})

	t.Run("put existing secret key returns error", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		sk, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteSecretKey(ctx)
		})

		// Clean up first and put the key
		_ = store.DeleteSecretKey(ctx)
		require.NoError(t, store.PutSecretKey(ctx, sk))

		// Try to put again
		sk2, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		err = store.PutSecretKey(ctx, sk2)
		require.ErrorIs(t, err, storage.ErrAlreadyExists)
	})
}

//nolint:paralleltest
func TestDeleteSecretKey_Integration(t *testing.T) {
	// Note: Secret key tests cannot run in parallel as they share the same path
	t.Run("delete non-existent secret key returns error", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		// Make sure it doesn't exist
		_ = store.DeleteSecretKey(ctx)

		err := store.DeleteSecretKey(ctx)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("delete existing secret key", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		sk, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		// Clean up first and put the key
		_ = store.DeleteSecretKey(ctx)
		require.NoError(t, store.PutSecretKey(ctx, sk))

		// Delete it
		require.NoError(t, store.DeleteSecretKey(ctx))

		// Verify it's gone
		_, err = store.GetSecretKey(ctx)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})
}

func TestHasNarInfo_Integration(t *testing.T) {
	t.Parallel()

	t.Run("no narinfo exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := testhelper.MustRandNarInfoHash()

		// Make sure it doesn't exist
		_ = store.DeleteNarInfo(ctx, hash)

		assert.False(t, store.HasNarInfo(ctx, hash))
	})

	t.Run("narinfo exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := testhelper.MustRandNarInfoHash()

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNarInfo(ctx, hash)
		})

		// Clean up first and put the narinfo
		_ = store.DeleteNarInfo(ctx, hash)
		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		assert.True(t, store.HasNarInfo(ctx, hash))
	})
}

func TestGetNarInfo_Integration(t *testing.T) {
	t.Parallel()

	t.Run("no narinfo exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := testhelper.MustRandNarInfoHash()

		// Make sure it doesn't exist
		_ = store.DeleteNarInfo(ctx, hash)

		_, err := store.GetNarInfo(ctx, hash)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("narinfo exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := testhelper.MustRandNarInfoHash()

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNarInfo(ctx, hash)
		})

		// Clean up first and put the narinfo
		_ = store.DeleteNarInfo(ctx, hash)
		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		ni2, err := store.GetNarInfo(ctx, hash)
		require.NoError(t, err)

		assert.Equal(t,
			strings.TrimSpace(testdata.Nar1.NarInfoText),
			strings.TrimSpace(ni2.String()),
		)
	})
}

func TestPutNarInfo_Integration(t *testing.T) {
	t.Parallel()

	t.Run("put narinfo successfully", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := testhelper.MustRandNarInfoHash()

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNarInfo(ctx, hash)
		})

		// Clean up first
		_ = store.DeleteNarInfo(ctx, hash)

		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		// Verify it was stored
		assert.True(t, store.HasNarInfo(ctx, hash))
	})

	t.Run("put existing narinfo returns error", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := testhelper.MustRandNarInfoHash()

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNarInfo(ctx, hash)
		})

		// Clean up first and put the narinfo
		_ = store.DeleteNarInfo(ctx, hash)
		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		// Try to put again
		err = store.PutNarInfo(ctx, hash, ni)
		require.ErrorIs(t, err, storage.ErrAlreadyExists)
	})
}

func TestDeleteNarInfo_Integration(t *testing.T) {
	t.Parallel()

	t.Run("delete non-existent narinfo returns error", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := testhelper.MustRandNarInfoHash()

		// Make sure it doesn't exist
		_ = store.DeleteNarInfo(ctx, hash)

		err := store.DeleteNarInfo(ctx, hash)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("delete existing narinfo", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := testhelper.MustRandNarInfoHash()

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Clean up first and put the narinfo
		_ = store.DeleteNarInfo(ctx, hash)
		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		// Delete it
		require.NoError(t, store.DeleteNarInfo(ctx, hash))

		// Verify it's gone
		assert.False(t, store.HasNarInfo(ctx, hash))
	})
}

func TestHasNar_Integration(t *testing.T) {
	t.Parallel()

	t.Run("no nar exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        testhelper.MustRandNarHash(),
			Compression: testdata.Nar1.NarCompression,
		}

		// Make sure it doesn't exist
		_ = store.DeleteNar(ctx, narURL)

		assert.False(t, store.HasNar(ctx, narURL))
	})

	t.Run("nar exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        testhelper.MustRandNarHash(),
			Compression: testdata.Nar1.NarCompression,
		}

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNar(ctx, narURL)
		})

		// Clean up first and put the nar
		_ = store.DeleteNar(ctx, narURL)
		_, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)

		assert.True(t, store.HasNar(ctx, narURL))
	})
}

func TestGetNar_Integration(t *testing.T) {
	t.Parallel()

	t.Run("no nar exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        testhelper.MustRandNarHash(),
			Compression: testdata.Nar1.NarCompression,
		}

		// Make sure it doesn't exist
		_ = store.DeleteNar(ctx, narURL)

		_, _, err := store.GetNar(ctx, narURL)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("nar exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        testhelper.MustRandNarHash(),
			Compression: testdata.Nar1.NarCompression,
		}

		// Clean up first and put the nar
		_ = store.DeleteNar(ctx, narURL)
		_, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNar(ctx, narURL)
		})

		size, r, err := store.GetNar(ctx, narURL)
		require.NoError(t, err)

		defer r.Close()

		content, err := io.ReadAll(r)
		require.NoError(t, err)

		assert.EqualValues(t, len(testdata.Nar1.NarText), size)
		assert.Equal(t, testdata.Nar1.NarText, string(content))
	})
}

func TestPutNar_Integration(t *testing.T) {
	t.Parallel()

	t.Run("put nar successfully", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        testhelper.MustRandNarHash(),
			Compression: testdata.Nar1.NarCompression,
		}

		// Clean up first
		_ = store.DeleteNar(ctx, narURL)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNar(ctx, narURL)
		})

		written, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)
		assert.EqualValues(t, len(testdata.Nar1.NarText), written)

		// Verify it was stored
		assert.True(t, store.HasNar(ctx, narURL))
	})

	t.Run("put existing nar returns error", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        testhelper.MustRandNarHash(),
			Compression: testdata.Nar1.NarCompression,
		}

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNar(ctx, narURL)
		})

		// Clean up first and put the nar
		_ = store.DeleteNar(ctx, narURL)
		_, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)

		// Try to put again
		_, err = store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.ErrorIs(t, err, storage.ErrAlreadyExists)
	})
}

func TestDeleteNar_Integration(t *testing.T) {
	t.Parallel()

	t.Run("delete non-existent nar returns error", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        testhelper.MustRandNarHash(),
			Compression: testdata.Nar1.NarCompression,
		}

		// Make sure it doesn't exist
		_ = store.DeleteNar(ctx, narURL)

		err := store.DeleteNar(ctx, narURL)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("delete existing nar", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        testhelper.MustRandNarHash(),
			Compression: testdata.Nar1.NarCompression,
		}

		// Clean up first and put the nar
		_ = store.DeleteNar(ctx, narURL)
		_, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)

		// Delete it
		require.NoError(t, store.DeleteNar(ctx, narURL))

		// Verify it's gone
		assert.False(t, store.HasNar(ctx, narURL))
	})
}

func TestWalkNarInfos_Integration(t *testing.T) {
	t.Parallel()

	store := getTestStore(t)
	if store == nil {
		return
	}

	ctx := newContext()
	hash1 := testhelper.MustRandNarInfoHash()
	hash2 := testhelper.MustRandNarInfoHash()

	ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)

	cfg := getTestConfig(t)
	require.NotNil(t, cfg)

	// Clean up and put narinfos
	_ = store.DeleteNarInfo(ctx, hash1)
	_ = store.DeleteNarInfo(ctx, hash2)
	require.NoError(t, store.PutNarInfo(ctx, hash1, ni))
	require.NoError(t, store.PutNarInfo(ctx, hash2, ni))

	t.Cleanup(func() {
		_ = store.DeleteNarInfo(ctx, hash1)
		_ = store.DeleteNarInfo(ctx, hash2)
	})

	foundHashes := make(map[string]bool)
	err = store.WalkNarInfos(ctx, func(hash string) error {
		foundHashes[hash] = true

		return nil
	})
	require.NoError(t, err)

	assert.True(t, foundHashes[hash1])
	assert.True(t, foundHashes[hash2])

	t.Run("callback error returns error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errCallbackFailed
		err = store.WalkNarInfos(ctx, func(_ string) error {
			return expectedErr
		})
		assert.ErrorIs(t, err, expectedErr)
	})

	t.Run("ignores non-narinfo files", func(t *testing.T) {
		t.Parallel()

		// Put a non-narinfo file in the prefix
		key := "store/narinfo/not-a-narinfo.txt"
		_, err := store.GetClient().PutObject(ctx, cfg.Bucket, key, strings.NewReader("content"), 7, minio.PutObjectOptions{})
		require.NoError(t, err)

		t.Cleanup(func() {
			_ = store.GetClient().RemoveObject(ctx, cfg.Bucket, key, minio.RemoveObjectOptions{})
		})

		err = store.WalkNarInfos(ctx, func(hash string) error {
			if hash == "not-a-narinfo" {
				t.Errorf("WalkNarInfos should have ignored non-narinfo file")
			}

			return nil
		})
		require.NoError(t, err)
	})
}

func TestNew_BucketAccessError(t *testing.T) {
	t.Parallel()

	cfg := getTestConfig(t)

	// Use mock transport to trigger error
	expectedErr := errTestingBucketAccess
	cfg.Transport = roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, expectedErr
	})

	_, err := storage_s3.New(newContext(), *cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error testing bucket access")
}

func TestNew_BucketNotFound(t *testing.T) {
	t.Parallel()

	cfg := getTestConfig(t)

	// Use a non-existent bucket
	cfg.Bucket = "non-existent-bucket-name-ncps-test"

	_, err := storage_s3.New(newContext(), *cfg)
	assert.ErrorIs(t, err, storage_s3.ErrBucketNotFound)
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
