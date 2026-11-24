-- Corpus: rebuilding objects within one migration, where re-running a
-- statement group must neither skip a needed CREATE nor drop what a later
-- statement made.

CREATE TABLE a (id bigint PRIMARY KEY, v varchar);
CREATE INDEX a_v ON a (v);
CREATE TABLE b (id bigint PRIMARY KEY);
INSERT INTO a (id, v) VALUES (1, 'x');

DROP INDEX a_v;
DROP TABLE a;
CREATE TABLE IF NOT EXISTS a (id bigint PRIMARY KEY, w varchar);
CREATE INDEX IF NOT EXISTS a_w ON a (w);
DROP TABLE b;
CREATE TABLE c (id bigint PRIMARY KEY);
INSERT INTO a (id, w) VALUES (2, 'y');

ALTER TABLE a ADD COLUMN x varchar;
ALTER TABLE a DROP COLUMN x;
ALTER TABLE a ADD COLUMN x bigint;
CREATE INDEX a_x ON a (x);
UPDATE a SET x = 7 WHERE id = 2;
