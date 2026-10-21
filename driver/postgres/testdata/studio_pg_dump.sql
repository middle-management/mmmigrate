--
-- PostgreSQL database dump
--

\restrict nenDb7Oncu4hFQRqfh2Ztnzg8ckIWk7fdlYKqe2bzKkSvg4xduvdakkK1b90EBC

-- Dumped from database version 14.24 (Ubuntu 14.24-1.pgdg24.04+2)
-- Dumped by pg_dump version 14.24 (Ubuntu 14.24-1.pgdg24.04+2)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: studio; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA studio;


--
-- Name: user_count(); Type: FUNCTION; Schema: studio; Owner: -
--

CREATE FUNCTION studio.user_count() RETURNS bigint
    LANGUAGE sql
    AS $$SELECT count(*) FROM studio."user"$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: user; Type: TABLE; Schema: studio; Owner: -
--

CREATE TABLE studio."user" (
    id uuid NOT NULL,
    email text NOT NULL
);


--
-- Name: active_user; Type: VIEW; Schema: studio; Owner: -
--

CREATE VIEW studio.active_user AS
 SELECT "user".id,
    "user".email
   FROM studio."user";


--
-- Name: user user_email_key; Type: CONSTRAINT; Schema: studio; Owner: -
--

ALTER TABLE ONLY studio."user"
    ADD CONSTRAINT user_email_key UNIQUE (email);


--
-- Name: user user_pkey; Type: CONSTRAINT; Schema: studio; Owner: -
--

ALTER TABLE ONLY studio."user"
    ADD CONSTRAINT user_pkey PRIMARY KEY (id);


--
-- PostgreSQL database dump complete
--

\unrestrict nenDb7Oncu4hFQRqfh2Ztnzg8ckIWk7fdlYKqe2bzKkSvg4xduvdakkK1b90EBC

