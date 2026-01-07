-- Ensure errors stop the script
\set ON_ERROR_STOP on

CREATE EXTENSION IF NOT EXISTS dblink;

-- 1. Define a foreign server using the psql variables
-- We drop CASCADE to ensure the user mapping is recreated cleanly if vars change
DROP SERVER IF EXISTS local_admin_server CASCADE;

CREATE SERVER local_admin_server
    FOREIGN DATA WRAPPER dblink_fdw
    OPTIONS (
        host   :'pghost',
        port   :'pgport',
        dbname :'dbname'
    );

-- 2. Map the current user (function owner) to the dblink user
-- This allows the SECURITY DEFINER function to use these credentials
CREATE USER MAPPING FOR current_user
    SERVER local_admin_server
    OPTIONS (user :'pguser');

-- 3. Function to CREATE database
-- Now we simply point dblink_exec to the server name 'local_admin_server'
CREATE OR REPLACE FUNCTION create_test_db(dbname text) RETURNS void AS $$
BEGIN
    IF left(dbname, 5) != 'test-' THEN
        RAISE EXCEPTION 'Access Denied: Database name must start with "test-"';
    END IF;

    PERFORM dblink_exec('local_admin_server', 'CREATE DATABASE ' || quote_ident(dbname) || ' OWNER "test-user"');
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- 4. Function to DROP database
CREATE OR REPLACE FUNCTION drop_test_db(dbname text) RETURNS void AS $$
BEGIN
    IF left(dbname, 5) != 'test-' THEN
        RAISE EXCEPTION 'Access Denied: Database name must start with "test-"';
    END IF;

    PERFORM dblink_exec('local_admin_server', 'DROP DATABASE ' || quote_ident(dbname));
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- 5. Lock down functions
REVOKE EXECUTE ON FUNCTION create_test_db(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION drop_test_db(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION create_test_db(text) TO "test-user";
GRANT EXECUTE ON FUNCTION drop_test_db(text) TO "test-user";
