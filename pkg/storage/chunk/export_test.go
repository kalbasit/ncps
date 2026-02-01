package chunk

import (
	"github.com/minio/minio-go/v7"

	"github.com/kalbasit/ncps/pkg/lock"
)

// GetClient returns the internal MinIO client.
// This is only for testing purposes.
func (s *s3Store) GetClient() *minio.Client {
	return s.client
}

// SetClient sets the internal MinIO client.
// This is only for testing purposes.
func (s *s3Store) SetClient(client *minio.Client) {
	s.client = client
}

// GetLocker returns the internal locker.
// This is only for testing purposes.
func (s *s3Store) GetLocker() lock.Locker {
	return s.locker
}

// SetLocker sets the internal locker.
// This is only for testing purposes.
func (s *s3Store) SetLocker(locker lock.Locker) {
	s.locker = locker
}
