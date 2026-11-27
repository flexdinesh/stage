-- +goose Up
CREATE TABLE observation_producers (
    machine_id bigint NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    producer text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    cursor bigint NOT NULL DEFAULT 0 CHECK (cursor >= 0),
    PRIMARY KEY (machine_id, producer)
);
CREATE UNIQUE INDEX observation_producers_active ON observation_producers(machine_id) WHERE active;

-- Loss counts use decimal text to preserve the full uint64 range.
CREATE TABLE observation_dispositions (
    machine_id bigint NOT NULL,
    producer text NOT NULL,
    cursor bigint NOT NULL CHECK (cursor > 0),
    code text NOT NULL,
    lost text NOT NULL,
    observed_from timestamptz,
    observed_to timestamptz,
    PRIMARY KEY (machine_id, producer, cursor),
    FOREIGN KEY (machine_id, producer) REFERENCES observation_producers(machine_id, producer) ON DELETE CASCADE
);
