package s3

import (
	"context"
	"fmt"
	"log"

	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

// Example demonstrates how to use the S3 storage implementation with AWS S3.
func Example() {
	ctx := context.Background()

	// Create S3 configuration for AWS S3
	cfg := Config{
		Bucket:          "my-nix-cache",
		Region:          "us-west-2", // Optional for AWS
		Endpoint:        "s3.us-west-2.amazonaws.com",
		AccessKeyID:     "your-access-key",
		SecretAccessKey: "your-secret-key",
		UseSSL:          true, // Always use SSL for AWS
	}

	// Create S3 store
	store, err := New(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to create S3 store: %v", err)
	}

	// The store implements the storage.Store interface
	// You can use it as a drop-in replacement for local storage

	// Example: Store a secret key
	secretKey, err := signature.LoadSecretKey("your-secret-key-content")
	if err != nil {
		log.Fatalf("Failed to load secret key: %v", err)
	}

	err = store.PutSecretKey(ctx, secretKey)
	if err != nil {
		log.Fatalf("Failed to put secret key: %v", err)
	}

	// Example: Check if a narinfo exists
	hash := "abc123"
	exists := store.HasNarInfo(ctx, hash)
	fmt.Printf("NarInfo %s exists: %t\n", hash, exists)

	// Example: Get the secret key back
	retrievedKey, err := store.GetSecretKey(ctx)
	if err != nil {
		log.Fatalf("Failed to get secret key: %v", err)
	}

	fmt.Printf("Retrieved secret key: %s\n", retrievedKey.String())
}

// ExampleMinIO demonstrates how to use the S3 storage with MinIO.
func ExampleMinIO() {
	ctx := context.Background()

	// Create MinIO configuration
	// For MinIO, the endpoint should be without the scheme
	cfg := Config{
		Bucket:          "my-nix-cache",
		Endpoint:        "localhost:9000", // Without scheme
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
		UseSSL:          false, // Set to false for local development without TLS
	}

	// Create S3 store
	store, err := New(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to create MinIO store: %v", err)
	}

	// Use the store as before
	fmt.Printf("MinIO store created successfully: %v\n", store)
}
