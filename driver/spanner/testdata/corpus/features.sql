-- Corpus: Spanner PostgreSQL-dialect DDL and DML features, and lexing edge
-- cases for the statement splitter: semicolons; in comments, /* block;
-- comments /* nested; */ still a comment; */ strings, dollar quotes.

CREATE TABLE singers (
  singer_id   bigint PRIMARY KEY,
  first_name  varchar(1024),
  last_name   varchar(1024) NOT NULL DEFAULT 'it''s; fine',
  full_name   varchar(2048) GENERATED ALWAYS AS (first_name || ' ' || last_name) STORED,
  info        jsonb,
  tags        text[],
  created     timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  score       numeric,
  active      boolean DEFAULT true,
  "MixedCase" bigint,
  CONSTRAINT chk_score CHECK (score >= 0)
);
/* A block comment; /* nested, as PostgreSQL allows; */ still the outer
   comment; */
CREATE TABLE albums (
  singer_id bigint NOT NULL,
  album_id  bigint NOT NULL,
  title     varchar,
  label     varchar,
  PRIMARY KEY (singer_id, album_id)
) INTERLEAVE IN PARENT singers ON DELETE CASCADE;
CREATE TABLE songs (
  singer_id bigint NOT NULL,
  album_id  bigint NOT NULL,
  song_id   bigint NOT NULL,
  title     varchar /* inline; comment */ NOT NULL,
  PRIMARY KEY (singer_id, album_id, song_id)
) INTERLEAVE IN PARENT albums ON DELETE CASCADE;
CREATE UNIQUE INDEX singers_by_name ON singers (last_name, first_name);
CREATE INDEX albums_by_title ON albums (title) INCLUDE (label);
CREATE INDEX songs_by_title ON songs (singer_id, album_id, title) INTERLEAVE IN albums;
CREATE INDEX singers_active ON singers (active) WHERE active IS NOT NULL;
CREATE INDEX singers_mixed ON singers ("MixedCase");
CREATE SEQUENCE ticket_seq BIT_REVERSED_POSITIVE;
CREATE TABLE tickets (
  id        bigint DEFAULT nextval('ticket_seq') PRIMARY KEY,
  singer_id bigint REFERENCES singers (singer_id),
  note      text
);
CREATE TABLE concerts (
  id        bigint PRIMARY KEY,
  singer_id bigint NOT NULL,
  venue     varchar DEFAULT $$O'Hare; Hall$$,
  CONSTRAINT fk_concert_singer FOREIGN KEY (singer_id) REFERENCES singers (singer_id) ON DELETE CASCADE
);
CREATE TABLE events (
  id bigint PRIMARY KEY,
  at timestamptz NOT NULL
) TTL INTERVAL '30 days' ON at;
CREATE VIEW singer_names SQL SECURITY INVOKER AS
  SELECT singer_id, full_name FROM singers WHERE last_name <> ';';
CREATE CHANGE STREAM singer_changes FOR singers;

-- DML between DDL groups: each statement runs exactly once.
INSERT INTO singers (singer_id, first_name, last_name, score) VALUES
  (1, 'A;B', $$C'D; "E"$$, 1), (2, E'it\'s; escaped', 'x', 2), (3, '/* not a comment */', '-- not either', 3);
INSERT INTO albums (singer_id, album_id, title) VALUES (1, 1, 'first'), (1, 2, $tag$sec;ond$tag$);
UPDATE singers SET score = score + 10 WHERE singer_id = 1;

ALTER TABLE singers ADD COLUMN nickname varchar;
ALTER TABLE singers ALTER COLUMN nickname SET DEFAULT 'none';
ALTER TABLE albums ADD COLUMN released date;
ALTER TABLE tickets ADD CONSTRAINT note_len CHECK (length(note) < 100);
CREATE TABLE scratch (id bigint PRIMARY KEY);
CREATE INDEX scratch_ids ON scratch (id);

UPDATE singers SET nickname = 'n' WHERE singer_id = 1;
DELETE FROM albums WHERE album_id = 2;

DROP INDEX singers_active;
ALTER TABLE singers DROP COLUMN info;
DROP INDEX scratch_ids;
DROP TABLE scratch;
CREATE SCHEMA app;
CREATE TABLE app.settings (k varchar PRIMARY KEY, v varchar);
CREATE INDEX settings_by_v ON app.settings (v);

INSERT INTO app.settings (k, v) VALUES ('a', '1'), ('b;c', '2');
