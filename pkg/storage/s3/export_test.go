package s3

import "github.com/minio/minio-go/v7"

// GetClient returns the internal MinIO client.
// This is only for testing purposes.
func (s *Store) GetClient() *minio.Client {
	return s.client
}
