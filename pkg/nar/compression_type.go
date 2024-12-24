package nar

import (
	"errors"
	"fmt"
)

// ErrUnknownFileExtension is returned if the file extension is not known.
var ErrUnknownFileExtension = errors.New("file extension is not known")

// CompressionType represents the compression types supported by Nix. See:
// https://github.com/NixOS/nix/blob/f1187cb696584739884687d788a6fbb4dd36c61c/src/libstore/binary-cache-store.cc#L166
type CompressionType string

const (
	CompressionTypeNone  CompressionType = "none"
	CompressionTypeBzip2 CompressionType = "bzip2"
	CompressionTypeZstd  CompressionType = "zstd"
	CompressionTypeLzip  CompressionType = "lzip"
	CompressionTypeLz4   CompressionType = "lz4"
	CompressionTypeBr    CompressionType = "br"
	CompressionTypeXz    CompressionType = "xz"
)

// CompressionTypeFromExtension returns the compression type given an extension.
func CompressionTypeFromExtension(ext string) (CompressionType, error) {
	switch ext {
	case "":
		fallthrough
	case "none":
		return CompressionTypeNone, nil
	case "bz2":
		return CompressionTypeBzip2, nil
	case "zst":
		return CompressionTypeZstd, nil
	case "lzip":
		return CompressionTypeLzip, nil
	case "lz4":
		return CompressionTypeLz4, nil
	case "br":
		return CompressionTypeBr, nil
	case "xz":
		return CompressionTypeXz, nil
	default:
		return CompressionType(""), ErrUnknownFileExtension
	}
}

// ToFileExtension returns the file extensions associated with the compression type.
func (ct CompressionType) ToFileExtension() string {
	switch ct {
	case CompressionType(""):
		fallthrough
	case CompressionTypeNone:
		return ""
	case CompressionTypeBzip2:
		return "bz2"
	case CompressionTypeZstd:
		return "zst"
	case CompressionTypeLzip:
		return "lzip"
	case CompressionTypeLz4:
		return "lz4"
	case CompressionTypeBr:
		return "br"
	case CompressionTypeXz:
		return "xz"
	default:
		panic(fmt.Sprintf("The compression type %s is not known", ct))
	}
}

// CompressionTypeFromString returns the string compression type as CompressionType.
func CompressionTypeFromString(ct string) CompressionType { return CompressionType(ct) }

// String returns the CompressionType as a string.
func (ct CompressionType) String() string { return string(ct) }
