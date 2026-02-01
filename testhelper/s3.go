package testhelper

import (
	"os"
	"testing"

	"github.com/kalbasit/ncps/pkg/s3"
)

// S3TestConfig returns the S3 configuration for testing.
// It skips the test if any required environment variable is missing.
func S3TestConfig(t *testing.T) *s3.Config {
	t.Helper()

	endpoint := os.Getenv("NCPS_TEST_S3_ENDPOINT")
	bucket := os.Getenv("NCPS_TEST_S3_BUCKET")
	region := os.Getenv("NCPS_TEST_S3_REGION")
	accessKeyID := os.Getenv("NCPS_TEST_S3_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("NCPS_TEST_S3_SECRET_ACCESS_KEY")

	if endpoint == "" || bucket == "" || region == "" || accessKeyID == "" || secretAccessKey == "" {
		t.Skip("Skipping S3 integration test: S3 environment variables not set")

		return nil
	}

	return &s3.Config{
		Bucket:          bucket,
		Region:          region,
		Endpoint:        endpoint,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		ForcePathStyle:  true,
	}
}
