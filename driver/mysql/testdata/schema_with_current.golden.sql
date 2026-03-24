-- server: 8.4.8

-- table: mmmigrate_applied
  version int NOT NULL
  name varchar(255) NOT NULL
  applied_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP

-- table: mmmigrate_current
  id int NOT NULL DEFAULT 1
  checksum varchar(64) NOT NULL
  applied_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP

-- table: posts
  id int NOT NULL
  user_id int NOT NULL
  title text NOT NULL
  body text
  published_at text

-- table: users
  id int NOT NULL
  name text NOT NULL
  email text
  bio text

