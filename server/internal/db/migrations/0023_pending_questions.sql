-- +goose Up

-- Pending user questions (docs/api.md "Pending questions"). Sync stores how
-- many of a thread's pending requests are answerable user-input questions
-- (the rest are approvals, handled in T3). question_responses records answers
-- stage sent, so a retried submit is idempotent.
ALTER TABLE threads ADD COLUMN pending_question_count integer NOT NULL DEFAULT 0;
CREATE TABLE question_responses (
    t3_thread_id text NOT NULL,
    request_id text NOT NULL,
    answers jsonb NOT NULL,
    responded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (t3_thread_id, request_id)
);

-- +goose Down
DROP TABLE question_responses;
ALTER TABLE threads DROP COLUMN pending_question_count;
