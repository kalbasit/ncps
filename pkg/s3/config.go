package s3

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

var (
	// ErrBucketRequired is returned if the bucket name is missing.
	ErrBucketRequired = errors.New("bucket name is required")

	// ErrEndpointRequired is returned if the endpoint is missing.
	ErrEndpointRequired = errors.New("endpoint is required")

	// ErrAccessKeyIDRequired is returned if the access key ID is missing.
	ErrAccessKeyIDRequired = errors.New("access key ID is required")

	// ErrSecretAccessKeyRequired is returned if the secret access key is missing.
	ErrSecretAccessKeyRequired = errors.New("secret access key is required")

	// ErrInvalidEndpointScheme is returned if the endpoint scheme is missing or invalid.
	ErrInvalidEndpointScheme = errors.New("S3 endpoint must include scheme (http:// or https://)")
)

// Config holds the configuration for S3 storage.
type Config struct {
	// Bucket is the S3 bucket name
	Bucket string
	// Region is the AWS region (optional)
	Region string
	// Endpoint is the S3-compatible endpoint URL with scheme (http:// or https://)
	Endpoint string
	// AccessKeyID is the access key for authentication
	AccessKeyID string
	// SecretAccessKey is the secret key for authentication
	SecretAccessKey string
	// ForcePathStyle forces path-style addressing (bucket.s3.com/key vs s3.com/bucket/key)
	// Set to true for MinIO and other S3-compatible services
	// Set to false for AWS S3 (default)
	ForcePathStyle bool
	// Prefix is an optional path prefix for all keys stored in the bucket
	Prefix string
	// Transport is the HTTP transport to use (optional, used for testing)
	Transport http.RoundTripper
}

// ValidateConfig validates the S3 configuration.
func ValidateConfig(cfg Config) error {
	if cfg.Bucket == "" {
		return ErrBucketRequired
	}

	if cfg.Endpoint == "" {
		return ErrEndpointRequired
	}

	// Ensure endpoint has a scheme
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: %s", ErrInvalidEndpointScheme, cfg.Endpoint)
	}

	if cfg.AccessKeyID == "" {
		return ErrAccessKeyIDRequired
	}

	if cfg.SecretAccessKey == "" {
		return ErrSecretAccessKeyRequired
	}

	return nil
}

// GetEndpointWithoutScheme returns the endpoint without the scheme prefix.
// This is useful since MinIO SDK expects endpoint without scheme.
func GetEndpointWithoutScheme(endpoint string) string {
	// This function assumes a valid URL with a scheme, as validated by ValidateConfig.
	// We can ignore the error from url.Parse.
	u, _ := url.Parse(endpoint)

	return u.Host
}

// IsHTTPS returns true if the endpoint uses HTTPS.
func IsHTTPS(endpoint string) bool {
	u, _ := url.Parse(endpoint)

	return u.Scheme == "https"
}
