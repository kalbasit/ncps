#!/usr/bin/env bash

set -e

# Remove stale marker file from previous runs
rm -f /tmp/ncps-minio-ready

# 1. Setup Admin Alias (Strictly use 127.0.0.1)
mc alias set local http://127.0.0.1:9000 admin password
# 2. Setup Resources
mc mb local/test-bucket || true
mc admin user svcacct add \
  --access-key "test-access-key" \
  --secret-key "test-secret-key" \
  local admin || true
echo "This is secret data!" | mc pipe --quiet local/test-bucket/message.txt
echo "---------------------------------------------------"
echo "🔍 VERIFICATION CHECKS:"
# Check A: Keys (mc handles signing internally)
echo -n "1. Access Keys... "
mc alias set tester http://127.0.0.1:9000 test-access-key test-secret-key > /dev/null
if mc ls tester/test-bucket/message.txt > /dev/null; then
  echo "✅ Success"
else
  echo "❌ Failed"
fi
# Check B: Public Access (Anonymous)
echo -n "2. Public Access (Expect 403)... "
HTTP_CODE=$(curl -o /dev/null --silent --head --write-out '%{http_code}\n' "http://127.0.0.1:9000/test-bucket/message.txt")
if [ "$HTTP_CODE" -eq "403" ]; then
   echo "✅ Success"
else
   echo "❌ Failed (Got $HTTP_CODE)"
fi
# Check C: Signed URL
echo -n "3. Signed Access... "
# Generate presigned URL using mc
SIGNED_URL=$(mc share download --expire 1h --json tester/test-bucket/message.txt | jq -r '.share')

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
fi
echo "---------------------------------------------------"
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║           NCPS MINIO CONFIGURATION                        ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""
echo "📦 S3-Compatible Storage Configuration:"
echo ""
echo "  Endpoint:     http://127.0.0.1:9000"
echo "  Region:       us-east-1"
echo "  Bucket:       test-bucket"
echo "  Access Key:   test-access-key"
echo "  Secret Key:   test-secret-key"
echo "  Use SSL:      false"
echo ""
echo "🌐 Console UI:"
echo "  URL:          http://127.0.0.1:9001"
echo "  Username:     admin"
echo "  Password:     password"
echo ""
echo "---------------------------------------------------"

# Create ready marker file for process-compose health check
touch /tmp/ncps-minio-ready

sleep infinity
