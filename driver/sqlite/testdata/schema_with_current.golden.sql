-- server: sqlite 3.51.3

-- table: mmmigrate_applied
CREATE TABLE mmmigrate_applied (
			namespace   TEXT NOT NULL,
			version     INTEGER NOT NULL,
			name        TEXT NOT NULL,
			applied_at  TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (namespace, version)
		);

-- table: mmmigrate_current
CREATE TABLE mmmigrate_current (
			namespace   TEXT PRIMARY KEY,
			checksum    TEXT NOT NULL,
			applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
		);

-- table: posts
CREATE TABLE posts (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    body TEXT
, published_at TEXT);

-- table: users
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT
, bio TEXT);
