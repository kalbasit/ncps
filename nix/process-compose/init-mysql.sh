#!/usr/bin/env bash

set -e

# Wait for MySQL/MariaDB to be ready
sleep 3

echo "Creating test user and database..."
mysql -h 127.0.0.1 -P 3306 --protocol=TCP -u root <<EOF
CREATE DATABASE IF NOT EXISTS \`test-db\`;
CREATE USER IF NOT EXISTS 'test-user'@'localhost' IDENTIFIED BY 'test-password';
CREATE USER IF NOT EXISTS 'test-user'@'127.0.0.1' IDENTIFIED BY 'test-password';
CREATE USER IF NOT EXISTS 'test-user'@'%' IDENTIFIED BY 'test-password';
GRANT ALL PRIVILEGES ON \`test-db\`.* TO 'test-user'@'localhost';
GRANT ALL PRIVILEGES ON \`test-db\`.* TO 'test-user'@'127.0.0.1';
GRANT ALL PRIVILEGES ON \`test-db\`.* TO 'test-user'@'%';
FLUSH PRIVILEGES;
EOF

echo "Verifying user creation..."
mysql -h 127.0.0.1 -P 3306 --protocol=TCP -u root -e "SELECT user, host FROM mysql.user WHERE user='test-user';" || echo "Failed to query users"

echo "---------------------------------------------------"
echo "🔍 VERIFICATION CHECKS:"

# Check A: Connection with test credentials
echo -n "1. Connection Test... "
if mysql -h 127.0.0.1 -P 3306 --protocol=TCP -u test-user -ptest-password test-db -e "SELECT 1" > /dev/null 2>&1; then
  echo "✅ Success"
else
  echo "❌ Failed"
  echo "   Attempting to connect with error output..."
  mysql -h 127.0.0.1 -P 3306 --protocol=TCP -u test-user -ptest-password test-db -e "SELECT 1" 2>&1 || true
  exit 1
fi

# Check B: Create test table and insert data
echo -n "2. Table Operations... "
if mysql -h 127.0.0.1 -P 3306 --protocol=TCP -u test-user -ptest-password test-db <<EOF > /dev/null 2>&1
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

# Check C: Query test data
echo -n "3. Query Test... "
RESULT=$(mysql -h 127.0.0.1 -P 3306 --protocol=TCP -u test-user -ptest-password test-db -N -e "SELECT message FROM test_table WHERE message = 'Test data'" 2>/dev/null)
if [ "$RESULT" = "Test data" ]; then
  echo "✅ Success"
  echo "   Content verified: ✅"
else
  echo "❌ Failed"
  echo "   Expected: 'Test data', Got: '$RESULT'"
fi

echo "---------------------------------------------------"
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║           NCPS MYSQL/MARIADB CONFIGURATION                ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""
echo "🗄️  MySQL/MariaDB Database Configuration:"
echo ""
echo "  Host:         127.0.0.1"
echo "  Port:         3306"
echo "  Database:     test-db"
echo "  Username:     test-user"
echo "  Password:     test-password"
echo ""
echo "  Root User:    root (no password - dev environment)"
echo ""
echo "🔗 Connection URL:"
echo "  mysql://test-user:test-password@127.0.0.1:3306/test-db"
echo ""
echo "💡 Environment Variables (for testing):"
echo "  export NCPS_TEST_MYSQL_URL=\"mysql://test-user:test-password@127.0.0.1:3306/test-db\""
echo ""
echo "---------------------------------------------------"

sleep infinity
