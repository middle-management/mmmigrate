-- table: mmmigrate_applied
  version int(11) NOT NULL
  name varchar(255) NOT NULL
  applied_at timestamp NOT NULL DEFAULT current_timestamp()

-- table: posts
  id int(11) NOT NULL
  user_id int(11) NOT NULL
  title text NOT NULL
  body text DEFAULT NULL
  published_at text DEFAULT NULL

-- table: users
  id int(11) NOT NULL
  name text NOT NULL
  email text DEFAULT NULL

