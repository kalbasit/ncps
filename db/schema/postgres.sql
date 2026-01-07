SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: dblink; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS dblink WITH SCHEMA public;


--
-- Name: EXTENSION dblink; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION dblink IS 'connect to other PostgreSQL databases from within a database';


--
-- Name: create_test_db(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.create_test_db(dbname text) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER
    AS $$
BEGIN
    IF left(dbname, 5) != 'test-' THEN
        RAISE EXCEPTION 'Access Denied: Database name must start with "test-"';
    END IF;
    -- Execute as superuser via local connection
    PERFORM dblink_exec('host=127.0.0.1 port=5432 dbname=postgres user=postgres', 'CREATE DATABASE ' || quote_ident(dbname) || ' OWNER "test-user"');
END;
$$;


--
-- Name: drop_test_db(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.drop_test_db(dbname text) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER
    AS $$
BEGIN
    IF left(dbname, 5) != 'test-' THEN
        RAISE EXCEPTION 'Access Denied: Database name must start with "test-"';
    END IF;
    -- Execute as superuser via local connection
    PERFORM dblink_exec('host=127.0.0.1 port=5432 dbname=postgres user=postgres', 'DROP DATABASE ' || quote_ident(dbname));
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: config; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.config (
    id bigint NOT NULL,
    key text NOT NULL,
    value text NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp with time zone
);


--
-- Name: config_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.config_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: config_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.config_id_seq OWNED BY public.config.id;


--
-- Name: nar_files; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.nar_files (
    id bigint NOT NULL,
    hash text NOT NULL,
    compression text DEFAULT ''::text NOT NULL,
    file_size bigint NOT NULL,
    query text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp with time zone,
    last_accessed_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT nar_files_file_size_check CHECK ((file_size >= 0))
);


--
-- Name: nar_files_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.nar_files_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: nar_files_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.nar_files_id_seq OWNED BY public.nar_files.id;


--
-- Name: narinfo_nar_files; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.narinfo_nar_files (
    narinfo_id bigint NOT NULL,
    nar_file_id bigint NOT NULL
);


--
-- Name: narinfos; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.narinfos (
    id bigint NOT NULL,
    hash text NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp with time zone,
    last_accessed_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);


--
-- Name: narinfos_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.narinfos_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: narinfos_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.narinfos_id_seq OWNED BY public.narinfos.id;


--
-- Name: schema_migrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.schema_migrations (
    version character varying NOT NULL
);


--
-- Name: config id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.config ALTER COLUMN id SET DEFAULT nextval('public.config_id_seq'::regclass);


--
-- Name: nar_files id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.nar_files ALTER COLUMN id SET DEFAULT nextval('public.nar_files_id_seq'::regclass);


--
-- Name: narinfos id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.narinfos ALTER COLUMN id SET DEFAULT nextval('public.narinfos_id_seq'::regclass);


--
-- Name: config config_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.config
    ADD CONSTRAINT config_key_key UNIQUE (key);


--
-- Name: config config_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.config
    ADD CONSTRAINT config_pkey PRIMARY KEY (id);


--
-- Name: nar_files nar_files_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.nar_files
    ADD CONSTRAINT nar_files_hash_key UNIQUE (hash);


--
-- Name: nar_files nar_files_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.nar_files
    ADD CONSTRAINT nar_files_pkey PRIMARY KEY (id);


--
-- Name: narinfo_nar_files narinfo_nar_files_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.narinfo_nar_files
    ADD CONSTRAINT narinfo_nar_files_pkey PRIMARY KEY (narinfo_id, nar_file_id);


--
-- Name: narinfos narinfos_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.narinfos
    ADD CONSTRAINT narinfos_hash_key UNIQUE (hash);


--
-- Name: narinfos narinfos_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.narinfos
    ADD CONSTRAINT narinfos_pkey PRIMARY KEY (id);


--
-- Name: schema_migrations schema_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (version);


--
-- Name: idx_nar_files_last_accessed_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_nar_files_last_accessed_at ON public.nar_files USING btree (last_accessed_at);


--
-- Name: idx_narinfo_nar_files_nar_file_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_narinfo_nar_files_nar_file_id ON public.narinfo_nar_files USING btree (nar_file_id);


--
-- Name: idx_narinfos_last_accessed_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_narinfos_last_accessed_at ON public.narinfos USING btree (last_accessed_at);


--
-- Name: narinfo_nar_files narinfo_nar_files_nar_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.narinfo_nar_files
    ADD CONSTRAINT narinfo_nar_files_nar_file_id_fkey FOREIGN KEY (nar_file_id) REFERENCES public.nar_files(id) ON DELETE CASCADE;


--
-- Name: narinfo_nar_files narinfo_nar_files_narinfo_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.narinfo_nar_files
    ADD CONSTRAINT narinfo_nar_files_narinfo_id_fkey FOREIGN KEY (narinfo_id) REFERENCES public.narinfos(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--



--
-- Dbmate schema migrations
--

INSERT INTO public.schema_migrations (version) VALUES
    ('20260101000000');
