CREATE EXTENSION IF NOT EXISTS dblink;

-- Function to CREATE database
CREATE OR REPLACE FUNCTION create_test_db(dbname text) RETURNS void AS $$
BEGIN
    IF left(dbname, 5) != 'test-' THEN
        RAISE EXCEPTION 'Access Denied: Database name must start with "test-"';
    END IF;
    -- Execute as superuser via local connection
    PERFORM dblink_exec('host={PGHOST} port={PGPORT} dbname=postgres user=postgres', 'CREATE DATABASE ' || quote_ident(dbname) || ' OWNER "test-user"');
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Function to DROP database
CREATE OR REPLACE FUNCTION drop_test_db(dbname text) RETURNS void AS $$
BEGIN
    IF left(dbname, 5) != 'test-' THEN
        RAISE EXCEPTION 'Access Denied: Database name must start with "test-"';
    END IF;
    -- Execute as superuser via local connection
    PERFORM dblink_exec('host={PGHOST} port={PGPORT} dbname=postgres user=postgres', 'DROP DATABASE ' || quote_ident(dbname));
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Lock down functions
REVOKE EXECUTE ON FUNCTION create_test_db(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION drop_test_db(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION create_test_db(text) TO "test-user";
GRANT EXECUTE ON FUNCTION drop_test_db(text) TO "test-user";
