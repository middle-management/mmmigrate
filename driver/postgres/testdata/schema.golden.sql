-- table: mmmigrate.applied
  version integer NOT NULL
  name text NOT NULL
  applied_at timestamp with time zone NOT NULL DEFAULT now()

-- table: public.posts
  id integer NOT NULL
  user_id integer NOT NULL
  title text NOT NULL
  body text
  published_at text

-- table: public.users
  id integer NOT NULL
  name text NOT NULL
  email text

