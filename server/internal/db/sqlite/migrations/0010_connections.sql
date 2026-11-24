-- +goose Up
CREATE TABLE opencode_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 endpoint TEXT NOT NULL DEFAULT '',
 browser_endpoint TEXT NOT NULL DEFAULT '',
 web_client TEXT NOT NULL DEFAULT '',
 username TEXT NOT NULL DEFAULT '',
 password TEXT NOT NULL DEFAULT '',
 verified_endpoint TEXT NOT NULL DEFAULT ''
);
INSERT INTO opencode_settings(id) VALUES(1);

CREATE TABLE shared_paths (
 connection_id INTEGER NOT NULL REFERENCES harness_connections(id),
 harness_root TEXT NOT NULL,
 stage_root TEXT NOT NULL,
 PRIMARY KEY(connection_id,harness_root)
);
