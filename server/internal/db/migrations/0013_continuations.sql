-- +goose Up
ALTER TABLE threads ADD COLUMN archived boolean NOT NULL DEFAULT false;
ALTER TABLE threads ADD COLUMN active_run_id text;
ALTER TABLE threads ADD COLUMN latest_run_id text;
ALTER TABLE threads ADD COLUMN latest_run_status text;

CREATE TABLE continuations (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  order_id uuid NOT NULL DEFAULT gen_random_uuid(),
  after_thread_id text NOT NULL REFERENCES threads(t3_thread_id),
  target_thread_id text NOT NULL REFERENCES threads(t3_thread_id),
  previous_continuation_id bigint UNIQUE REFERENCES continuations(id),
  prompt text NOT NULL CHECK (length(btrim(prompt)) > 0),
  state text NOT NULL DEFAULT 'waiting' CHECK (state IN
    ('waiting','paused','blocked','sending','running','awaiting_settle','settled','cancelled','uncertain')),
  wait_reasons text[] NOT NULL DEFAULT '{}',
  last_error text,
  request_id text UNIQUE,
  delivery_prompt text,
  claim_expires_at timestamptz,
  run_id text,
  message_id text,
  run_completed boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  sent_at timestamptz,
  settled_at timestamptz,
  UNIQUE(order_id, after_thread_id),
  UNIQUE(order_id, target_thread_id),
  CHECK (after_thread_id <> target_thread_id)
);
CREATE TABLE continuation_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  continuation_id bigint NOT NULL REFERENCES continuations(id) ON DELETE CASCADE,
  state text NOT NULL,
  detail text NOT NULL DEFAULT '',
  at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX continuation_events_order ON continuation_events(continuation_id, id);
CREATE INDEX continuation_pending ON continuations(id) WHERE state NOT IN ('settled','cancelled');
CREATE INDEX continuation_order ON continuations(order_id,id);

-- +goose Down
DROP TABLE continuation_events;
DROP TABLE continuations;
ALTER TABLE threads DROP COLUMN latest_run_status, DROP COLUMN latest_run_id,
  DROP COLUMN active_run_id, DROP COLUMN archived;
