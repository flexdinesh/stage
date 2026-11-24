-- +goose Up
-- Search indexes (#404, docs/storage.md "Search"): FTS5 trigram
-- external-content tables over the searched columns, case-insensitive
-- (Unicode case folding, no diacritic removal). Triggers keep them in step
-- with every write, FK cascade deletes included; a migration that rebuilds
-- tasks or journal_entries must recreate these triggers and rebuild the
-- index. Queries shorter than 3 characters use LIKE instead (search.go).

CREATE VIRTUAL TABLE tasks_fts USING fts5(title, prompt, content='tasks', content_rowid='id', tokenize='trigram case_sensitive 0');

-- +goose StatementBegin
CREATE TRIGGER tasks_fts_insert AFTER INSERT ON tasks BEGIN
 INSERT INTO tasks_fts(rowid, title, prompt) VALUES (NEW.id, NEW.title, NEW.prompt);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_fts_delete AFTER DELETE ON tasks BEGIN
 INSERT INTO tasks_fts(tasks_fts, rowid, title, prompt) VALUES ('delete', OLD.id, OLD.title, OLD.prompt);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_fts_update AFTER UPDATE OF title, prompt ON tasks BEGIN
 INSERT INTO tasks_fts(tasks_fts, rowid, title, prompt) VALUES ('delete', OLD.id, OLD.title, OLD.prompt);
 INSERT INTO tasks_fts(rowid, title, prompt) VALUES (NEW.id, NEW.title, NEW.prompt);
END;
-- +goose StatementEnd

INSERT INTO tasks_fts(tasks_fts) VALUES ('rebuild');

CREATE VIRTUAL TABLE journal_fts USING fts5(text, content='journal_entries', content_rowid='id', tokenize='trigram case_sensitive 0');

-- +goose StatementBegin
CREATE TRIGGER journal_fts_insert AFTER INSERT ON journal_entries BEGIN
 INSERT INTO journal_fts(rowid, text) VALUES (NEW.id, NEW.text);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_fts_delete AFTER DELETE ON journal_entries BEGIN
 INSERT INTO journal_fts(journal_fts, rowid, text) VALUES ('delete', OLD.id, OLD.text);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER journal_fts_update AFTER UPDATE OF text ON journal_entries BEGIN
 INSERT INTO journal_fts(journal_fts, rowid, text) VALUES ('delete', OLD.id, OLD.text);
 INSERT INTO journal_fts(rowid, text) VALUES (NEW.id, NEW.text);
END;
-- +goose StatementEnd

INSERT INTO journal_fts(journal_fts) VALUES ('rebuild');
