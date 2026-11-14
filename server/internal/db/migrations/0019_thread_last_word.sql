-- +goose Up

-- Last word: a thread's latest assistant reply, read by sync when the thread
-- is at rest and its updated_at changed. text is a capped excerpt
-- (truncated = the reply is longer); preview is plain text for list rows;
-- at is the reply item's updatedAt. checked_at is the thread updated_at the
-- last successful check saw (the re-read trigger).
ALTER TABLE threads
    ADD COLUMN last_word_text text,
    ADD COLUMN last_word_preview text,
    ADD COLUMN last_word_truncated boolean NOT NULL DEFAULT false,
    ADD COLUMN last_word_run_id text,
    ADD COLUMN last_word_item_id text,
    ADD COLUMN last_word_at timestamptz,
    ADD COLUMN last_word_checked_at timestamptz;

-- +goose Down
ALTER TABLE threads
    DROP COLUMN last_word_text,
    DROP COLUMN last_word_preview,
    DROP COLUMN last_word_truncated,
    DROP COLUMN last_word_run_id,
    DROP COLUMN last_word_item_id,
    DROP COLUMN last_word_at,
    DROP COLUMN last_word_checked_at;
