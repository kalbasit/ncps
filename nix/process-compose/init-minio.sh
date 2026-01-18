#!/usr/bin/env bash

set -euo pipefail

# ---------------------------------------------------
# SETUP: Local admin alias for the connection
# ---------------------------------------------------
mc alias set local "$MINIO_ENDPOINT" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"

# ---------------------------------------------------
# SETUP: Create the test bucket and the access key
# ---------------------------------------------------
mc mb "local/$MINIO_TEST_S3_BUCKET" || true
mc admin user svcacct add \
  --access-key "$MINIO_TEST_S3_ACCESS_KEY_ID" \
  --secret-key "$MINIO_TEST_S3_SECRET_ACCESS_KEY" \
  local "$MINIO_ROOT_USER" || true

# ---------------------------------------------------
# Check access key can read/write to test bucket
# ---------------------------------------------------
echo "This is secret data!" | mc pipe --quiet "local/$MINIO_TEST_S3_BUCKET/message.txt"
echo "---------------------------------------------------"
echo "🔍 VERIFICATION CHECKS:"
# Check A: Keys (mc handles signing internally)
echo -n "1. Access Keys... "
mc alias set tester "$MINIO_ENDPOINT" "$MINIO_TEST_S3_ACCESS_KEY_ID" "$MINIO_TEST_S3_SECRET_ACCESS_KEY" > /dev/null
if mc ls "local/$MINIO_TEST_S3_BUCKET/message.txt" > /dev/null; then
  echo "✅ Success"
else
  echo "❌ Failed"
fi
# Check B: Public Access (Anonymous)
echo -n "2. Public Access (Expect 403)... "
HTTP_CODE=$(curl -o /dev/null --silent --head --write-out '%{http_code}\n' "$MINIO_ENDPOINT/$MINIO_TEST_S3_BUCKET/message.txt")
if [ "$HTTP_CODE" -eq "403" ]; then
   echo "✅ Success"
else
   echo "❌ Failed (Got $HTTP_CODE)"
fi
# Check C: Signed URL
echo -n "3. Signed Access... "
# Generate presigned URL using mc
SIGNED_URL=$(mc share download --expire 1h --json "tester/$MINIO_TEST_S3_BUCKET/message.txt" | jq -r '.share')

# Test the signed URL as-is (no Host header manipulation)
if curl --output /dev/null --silent --fail "$SIGNED_URL"; then
  echo "✅ Success"
  # Verify content
  CONTENT=$(curl --silent "$SIGNED_URL")
  if [ "$CONTENT" = "This is secret data!" ]; then
    echo "   Content verified: ✅"
  else
    echo "   Content mismatch: ❌"
  fi
else
  echo "❌ Failed"
  echo "Debug URL: $SIGNED_URL"
  echo "Testing with verbose output:"
  curl -v "$SIGNED_URL" 2>&1 | tail -20
  exit 1
fi
echo "---------------------------------------------------"
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║           NCPS MINIO CONFIGURATION                        ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""
echo "📦 S3-Compatible Storage Configuration:"
echo ""
echo "  Endpoint:     $MINIO_ENDPOINT"
echo "  Region:       $MINIO_REGION"
echo "  Bucket:       $MINIO_TEST_S3_BUCKET"
echo "  Access Key:   $MINIO_TEST_S3_ACCESS_KEY_ID"
echo "  Secret Key:   $MINIO_TEST_S3_SECRET_ACCESS_KEY"
echo "  Use SSL:      false"
echo ""
echo "🌐 Console UI:"
echo "  URL:          $MINIO_CONSOLE"
echo "  Username:     $MINIO_ROOT_USER"
echo "  Password:     $MINIO_ROOT_PASSWORD"
echo ""
echo "---------------------------------------------------"
