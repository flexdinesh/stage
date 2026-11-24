-- +goose Up
-- The local machine's seeded label 'This computer' (0015) named the viewer's
-- device, which is wrong from any other browser. An empty label now means
-- unset: each server start derives one (the hostname, or the public URL's
-- host in a container) and sets it only while it is still empty
-- (db.Store.SetDefaultLocalLabel). A rename never clears it, so a user's
-- name is kept.
UPDATE machines SET label='' WHERE id=1 AND label='This computer';
