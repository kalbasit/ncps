#!/usr/bin/env bash

set -euo pipefail

ncps_datadir="$(mktemp -d)"
trap "rm -rf $ncps_datadir" EXIT

mkdir -p "$ncps_datadir/var/ncps/db"

dbmate --url "sqlite:$ncps_datadir/var/ncps/db/db.sqlite" up

watchexec -e go -c clear -r go run . \
  serve \
  --cache-allow-put-verb \
  --cache-hostname localhost \
  --storage-s3-bucket test-bucket \
  --storage-s3-endpoint 127.0.0.1:9000 \
  --storage-s3-region us-east-1 \
  --storage-s3-access-key-id test-access-key \
  --storage-s3-secret-access-key test-secret-key \
  --storage-s3-use-ssl=false \
  --cache-database-url "sqlite:$ncps_datadir/var/ncps/db/db.sqlite" \
  --upstream-cache https://cache.nixos.org \
  --upstream-cache https://nix-community.cachix.org \
  --upstream-public-key cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY= \
  --upstream-public-key nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs= \
  "$@"
