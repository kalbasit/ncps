package s3_test

import (
	"errors"
	"testing"

	"github.com/kalbasit/ncps/pkg/s3"
)

func TestValidateConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     s3.Config
		wantErr error
	}{
		{
			name: "valid config http",
			cfg: s3.Config{
				Bucket:          "my-bucket",
				Endpoint:        "http://localhost:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
			},
			wantErr: nil,
		},
		{
			name: "valid config https",
			cfg: s3.Config{
				Bucket:          "my-bucket",
				Endpoint:        "https://s3.amazonaws.com",
				AccessKeyID:     "access",
				SecretAccessKey: "secret",
			},
			wantErr: nil,
		},
		{
			name: "missing bucket",
			cfg: s3.Config{
				Endpoint:        "http://localhost:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
			},
			wantErr: s3.ErrBucketRequired,
		},
		{
			name: "missing endpoint",
			cfg: s3.Config{
				Bucket:          "my-bucket",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
			},
			wantErr: s3.ErrEndpointRequired,
		},
		{
			name: "missing access key",
			cfg: s3.Config{
				Bucket:          "my-bucket",
				Endpoint:        "http://localhost:9000",
				SecretAccessKey: "minioadmin",
			},
			wantErr: s3.ErrAccessKeyIDRequired,
		},
		{
			name: "missing secret key",
			cfg: s3.Config{
				Bucket:      "my-bucket",
				Endpoint:    "http://localhost:9000",
				AccessKeyID: "minioadmin",
			},
			wantErr: s3.ErrSecretAccessKeyRequired,
		},
		{
			name: "invalid scheme",
			cfg: s3.Config{
				Bucket:          "my-bucket",
				Endpoint:        "ftp://localhost:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
			},
			wantErr: s3.ErrInvalidEndpointScheme,
		},
		{
			name: "no scheme",
			cfg: s3.Config{
				Bucket:          "my-bucket",
				Endpoint:        "localhost:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
			},
			wantErr: s3.ErrInvalidEndpointScheme,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := s3.ValidateConfig(tt.cfg)
			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("ValidateConfig() error = nil, wantErr %v", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateConfig() error = %v, wantErr nil", err)
				}
			}
		})
	}
}

func TestGetEndpointWithoutScheme(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			name:     "http endpoint",
			endpoint: "http://localhost:9000",
			want:     "localhost:9000",
		},
		{
			name:     "https endpoint",
			endpoint: "https://s3.amazonaws.com",
			want:     "s3.amazonaws.com",
		},
		{
			name:     "endpoint with path",
			endpoint: "http://localhost:9000/path",
			want:     "localhost:9000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := s3.GetEndpointWithoutScheme(tt.endpoint); got != tt.want {
				t.Errorf("GetEndpointWithoutScheme() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsHTTPS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		want     bool
	}{
		{
			name:     "http endpoint",
			endpoint: "http://localhost:9000",
			want:     false,
		},
		{
			name:     "https endpoint",
			endpoint: "https://s3.amazonaws.com",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := s3.IsHTTPS(tt.endpoint); got != tt.want {
				t.Errorf("IsHTTPS() = %v, want %v", got, tt.want)
			}
		})
	}
}
