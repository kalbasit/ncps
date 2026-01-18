#!/usr/bin/env bash

set -euo pipefail

# ---------------------------------------------------
# SETUP: Dev User (Standard)
# ---------------------------------------------------
echo "Creating dev user and database..."
mysql -h "$MYSQL_HOST" -u "$MYSQL_USER" <<EOF
CREATE DATABASE IF NOT EXISTS \`$MYSQL_DEV_DB\`;
CREATE USER IF NOT EXISTS '$MYSQL_DEV_USER'@'localhost' IDENTIFIED BY '$MYSQL_DEV_PASSWORD';
CREATE USER IF NOT EXISTS '$MYSQL_DEV_USER'@'$MYSQL_HOST' IDENTIFIED BY '$MYSQL_DEV_PASSWORD';
GRANT ALL PRIVILEGES ON \`$MYSQL_DEV_DB\`.* TO '$MYSQL_DEV_USER'@'localhost';
GRANT ALL PRIVILEGES ON \`$MYSQL_DEV_DB\`.* TO '$MYSQL_DEV_USER'@'$MYSQL_HOST';
FLUSH PRIVILEGES;
EOF

# ---------------------------------------------------
# SETUP: Migration User (Standard)
# ---------------------------------------------------
echo "Creating migration user and database..."
mysql -h "$MYSQL_HOST" -u "$MYSQL_USER" <<EOF
CREATE USER IF NOT EXISTS '$MYSQL_MIGRATION_USER'@'$MYSQL_HOST' IDENTIFIED BY '$MYSQL_MIGRATION_PASSWORD';
CREATE DATABASE IF NOT EXISTS \`$MYSQL_MIGRATION_DB\`;
GRANT ALL PRIVILEGES ON \`$MYSQL_MIGRATION_DB\`.* TO '$MYSQL_MIGRATION_USER'@'$MYSQL_HOST';
FLUSH PRIVILEGES;
EOF


# ---------------------------------------------------
# SETUP: Test User (Restricted Pattern)
# ---------------------------------------------------
echo "Creating test user and default $MYSQL_TEST_DB..."
mysql -h "$MYSQL_HOST" -u "$MYSQL_USER" <<EOF
CREATE USER IF NOT EXISTS '$MYSQL_TEST_USER'@'$MYSQL_HOST' IDENTIFIED BY '$MYSQL_TEST_PASSWORD';
-- Pre-create the default test database for initial connection
CREATE DATABASE IF NOT EXISTS \`$MYSQL_TEST_DB\`;
-- Grant ALL privileges on '$MYSQL_TEST_DB' and any other 'test-*' databases
GRANT ALL PRIVILEGES ON \`test-%\`.* TO '$MYSQL_TEST_USER'@'$MYSQL_HOST';
FLUSH PRIVILEGES;
EOF

echo "Verifying user creation..."
mysql -h "$MYSQL_HOST" -u "$MYSQL_USER" -e "SELECT user, host FROM mysql.user WHERE user IN ('$MYSQL_DEV_USER', '$MYSQL_TEST_USER');"

echo "Verifying migration user creation..."
mysql -h "$MYSQL_HOST" -u "$MYSQL_USER" -e "SELECT user, host FROM mysql.user WHERE user IN ('$MYSQL_MIGRATION_USER');"

echo "---------------------------------------------------"
echo "🔍 VERIFICATION CHECKS:"

# ---------------------------------------------------
# Check $MYSQL_DEV_USER connectivity and operations
# ---------------------------------------------------

echo -n "Dev Connection Test... "
if mysql -h "$MYSQL_HOST" -u "$MYSQL_DEV_USER" -p"$MYSQL_DEV_PASSWORD" "$MYSQL_DEV_DB" -e "SELECT 1" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Dev Table Operations... "
if mysql -h "$MYSQL_HOST" -u "$MYSQL_DEV_USER" -p"$MYSQL_DEV_PASSWORD" "$MYSQL_DEV_DB" <<EOF > /dev/null 2>&1
CREATE TABLE IF NOT EXISTS test_table (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  message TEXT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO test_table (message) VALUES ('Test data');
EOF
then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Dev Query Test... "
# Inlined assignment to prevent set -e from killing the script on failure
if RESULT="$(mysql -h "$MYSQL_HOST" -u "$MYSQL_DEV_USER" -p"$MYSQL_DEV_PASSWORD" "$MYSQL_DEV_DB" -N -e "SELECT message FROM test_table WHERE message = 'Test data'" 2>/dev/null)" && [ "$RESULT" = "Test data" ]; then
  echo "✅ Success"
  echo "   Content verified: ✅"
else
  echo "❌ Failed"
  echo "   Expected: 'Test data', Got: '$RESULT'"
  exit 1
fi

echo -n "   Clean up after Dev Table Operations... "
if mysql -h "$MYSQL_HOST" -u "$MYSQL_DEV_USER" -p"$MYSQL_DEV_PASSWORD" "$MYSQL_DEV_DB" -e "DROP TABLE IF EXISTS test_table;" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

# ---------------------------------------------------
# Check $MYSQL_TEST_USER connectivity, database create/drop
# ---------------------------------------------------

echo -n "Test Connection Test (to $MYSQL_TEST_DB)... "
# Now we verify we can connect directly to '$MYSQL_TEST_DB'
if mysql -h "$MYSQL_HOST" -u "$MYSQL_TEST_USER" -p"$MYSQL_TEST_PASSWORD" "$MYSQL_TEST_DB" -e "SELECT 1" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Test User Create Logic (test-123)... "
if mysql -h "$MYSQL_HOST" -u "$MYSQL_TEST_USER" -p"$MYSQL_TEST_PASSWORD" -e "CREATE DATABASE \`test-123\`;" > /dev/null 2>&1; then
    echo "✅ Success"
else
    echo "❌ Failed (Could not create database 'test-123')"
    exit 1
fi

echo -n "Test USER Table Operations... "
if mysql -h "$MYSQL_HOST" -u "$MYSQL_TEST_USER" -p"$MYSQL_TEST_PASSWORD" test-123 <<EOF > /dev/null 2>&1
CREATE TABLE IF NOT EXISTS test_table (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  message TEXT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO test_table (message) VALUES ('Test data');
EOF
then
  echo "✅ Success"
else
  echo "❌ Failed"
  exit 1
fi

echo -n "Test User Drop Logic (test-123)... "
if mysql -h "$MYSQL_HOST" -u "$MYSQL_TEST_USER" -p"$MYSQL_TEST_PASSWORD" -e "DROP DATABASE \`test-123\`;" > /dev/null 2>&1; then
    echo "✅ Success"
else
    echo "❌ Failed (Could not drop database 'test-123')"
    exit 1
fi

echo "---------------------------------------------------"
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║           NCPS MYSQL/MARIADB CONFIGURATION                ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""
echo "🗄️  Dev User (Standard):"
echo "  URL: mysql://$MYSQL_DEV_USER:$MYSQL_DEV_PASSWORD@$MYSQL_HOST:3306/$MYSQL_DEV_DB"
echo ""
echo "🧪 Test User (Integration Tests):"
echo "  URL: mysql://$MYSQL_TEST_USER:$MYSQL_TEST_PASSWORD@$MYSQL_HOST:3306/$MYSQL_TEST_DB"
echo "  Capabilities: Can CREATE/DROP any database starting with 'test-'"
echo "  Example: CREATE DATABASE \`test-foo\`;"
echo ""
echo "---------------------------------------------------"
