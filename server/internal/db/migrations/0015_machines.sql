-- +goose Up
-- A machine runs harnesses. Today one local machine owns every connection;
-- the column keeps connections, their workspaces and the hosting policy
-- machine-scoped for a future worker per machine (docs/architecture.md).
--
-- harness_connections is rebuilt to drop the fixed harness list (the
-- registry validates kinds in Go) and to scope the active and identity
-- indexes to a machine. stable_identity marks harnesses whose identity names
-- one instance for good (a T3 environment id): a retired connection with the
-- same identity is revived. Same rebuild procedure as 0012; legacy_alter_table
-- keeps triggers that name harness_connections valid across the rename.
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
PRAGMA legacy_alter_table=ON;
BEGIN;

CREATE TABLE machines (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 label TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT(stage_now())
);
INSERT INTO machines(id,label) VALUES(1,'This computer');

CREATE TABLE harness_connections_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 machine_id INTEGER NOT NULL DEFAULT 1 REFERENCES machines(id),
 harness TEXT NOT NULL CHECK(harness<>''),
 created_at TEXT NOT NULL DEFAULT(stage_now()),
 auto_complete INTEGER NOT NULL DEFAULT 1 CHECK(auto_complete IN (0,1)),
 -- The instance identity the harness's connector verified.
 identity TEXT NOT NULL DEFAULT '',
 stable_identity INTEGER NOT NULL DEFAULT 0 CHECK(stable_identity IN (0,1)),
 -- Display name of the instance.
 label TEXT NOT NULL DEFAULT '',
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
 retired_at TEXT,
 forgotten_at TEXT,
 CHECK(active=1 OR retired_at IS NOT NULL),
 CHECK(forgotten_at IS NULL OR active=0)
);
INSERT INTO harness_connections_new(id,machine_id,harness,created_at,auto_complete,identity,stable_identity,label,active,retired_at,forgotten_at)
 SELECT id,1,harness,created_at,auto_complete,identity,harness='t3',label,active,retired_at,forgotten_at FROM harness_connections;
UPDATE sqlite_sequence SET seq=(SELECT max(seq) FROM sqlite_sequence WHERE name IN ('harness_connections','harness_connections_new')) WHERE name='harness_connections_new';
DROP TABLE harness_connections;
ALTER TABLE harness_connections_new RENAME TO harness_connections;
CREATE UNIQUE INDEX harness_connection_active ON harness_connections(machine_id,harness) WHERE active=1;
CREATE UNIQUE INDEX harness_connection_identity ON harness_connections(machine_id,harness,identity) WHERE stable_identity=1 AND identity<>'';

COMMIT;
PRAGMA legacy_alter_table=OFF;
PRAGMA foreign_keys=ON;
