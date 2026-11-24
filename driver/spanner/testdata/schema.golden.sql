-- server: spanner (PostgreSQL dialect)

-- table: public.mmmigrate_applied
  namespace character varying NOT NULL
  version bigint NOT NULL
  name character varying NOT NULL
  applied_at timestamp with time zone NOT NULL

-- table: public.mmmigrate_current
  namespace character varying NOT NULL
  checksum character varying NOT NULL
  applied_at timestamp with time zone NOT NULL

-- table: public.mmmigrate_lock
  namespace character varying NOT NULL
  owner character varying NOT NULL
  expires_at timestamp with time zone NOT NULL

-- table: public.mmmigrate_progress
  namespace character varying NOT NULL
  version bigint NOT NULL
  done bigint NOT NULL
  prefix character varying NOT NULL
  fails character varying NOT NULL DEFAULT ''::text
  updated_at timestamp with time zone NOT NULL

-- table: public.posts
  id bigint NOT NULL
  user_id bigint NOT NULL
  title character varying NOT NULL
  body character varying
  published_at character varying

-- table: public.users
  id bigint NOT NULL
  name character varying NOT NULL
  email character varying

