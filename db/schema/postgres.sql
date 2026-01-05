\restrict EZf7FjC0sCKpCiS5gRXnnlqynWnwLeE8Il4MfcXQwc1SqDW9goJyd1ZMg1wX7Af

-- Dumped from database version 17.7
-- Dumped by pg_dump version 17.7

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

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: nar_files; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.nar_files (
    id bigint NOT NULL,
    hash text NOT NULL,
    compression text DEFAULT ''::text NOT NULL,
    file_size bigint NOT NULL,
    query text DEFAULT ''::text NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp without time zone,
    last_accessed_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
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
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp without time zone,
    last_accessed_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
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
-- Name: test_table; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.test_table (
    id integer NOT NULL,
    message text NOT NULL
);


--
-- Name: test_table_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.test_table_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: test_table_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.test_table_id_seq OWNED BY public.test_table.id;


--
-- Name: nar_files id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.nar_files ALTER COLUMN id SET DEFAULT nextval('public.nar_files_id_seq'::regclass);


--
-- Name: narinfos id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.narinfos ALTER COLUMN id SET DEFAULT nextval('public.narinfos_id_seq'::regclass);


--
-- Name: test_table id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.test_table ALTER COLUMN id SET DEFAULT nextval('public.test_table_id_seq'::regclass);


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
-- Name: test_table test_table_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.test_table
    ADD CONSTRAINT test_table_pkey PRIMARY KEY (id);


--
-- Name: idx_nar_files_last_accessed_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_nar_files_last_accessed_at ON public.nar_files USING btree (last_accessed_at);


--
-- Name: idx_narinfo_nar_files_nar_file_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_narinfo_nar_files_nar_file_id ON public.narinfo_nar_files USING btree (nar_file_id);


--
-- Name: idx_narinfo_nar_files_narinfo_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_narinfo_nar_files_narinfo_id ON public.narinfo_nar_files USING btree (narinfo_id);


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

\unrestrict EZf7FjC0sCKpCiS5gRXnnlqynWnwLeE8Il4MfcXQwc1SqDW9goJyd1ZMg1wX7Af


--
-- Dbmate schema migrations
--

INSERT INTO public.schema_migrations (version) VALUES
    ('20241210054814'),
    ('20241210054829'),
    ('20241213014846'),
    ('20251230224159'),
    ('20260105030513');
