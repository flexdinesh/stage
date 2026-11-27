-- +goose Up
CREATE TABLE observation_producers (
    machine_id INTEGER NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    producer TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    cursor INTEGER NOT NULL DEFAULT 0 CHECK (cursor >= 0),
    PRIMARY KEY (machine_id, producer)
);
CREATE UNIQUE INDEX observation_producers_active ON observation_producers(machine_id) WHERE active = 1;

-- Loss counts use decimal text to preserve the full uint64 range.
CREATE TABLE observation_dispositions (
    machine_id INTEGER NOT NULL,
    producer TEXT NOT NULL,
    cursor INTEGER NOT NULL CHECK (cursor > 0),
    code TEXT NOT NULL,
    lost TEXT NOT NULL,
    observed_from TEXT,
    observed_to TEXT,
    PRIMARY KEY (machine_id, producer, cursor),
    FOREIGN KEY (machine_id, producer) REFERENCES observation_producers(machine_id, producer) ON DELETE CASCADE
);
