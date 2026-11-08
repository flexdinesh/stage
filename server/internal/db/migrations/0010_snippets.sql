-- +goose Up

-- Templates become plain-text snippets, optionally scoped to one project.
-- Model-only presets (empty prompt) have no snippet meaning and are dropped.
DELETE FROM templates WHERE prompt = '';
ALTER TABLE templates RENAME TO snippets;
ALTER TABLE snippets RENAME CONSTRAINT templates_pkey TO snippets_pkey;
ALTER TABLE snippets RENAME CONSTRAINT templates_name_key TO snippets_name_key;
ALTER SEQUENCE templates_id_seq RENAME TO snippets_id_seq;
ALTER TABLE snippets RENAME COLUMN prompt TO body;
ALTER TABLE snippets ALTER COLUMN body DROP DEFAULT;
ALTER TABLE snippets ADD CONSTRAINT snippets_body_check CHECK (body <> '');
ALTER TABLE snippets
    DROP COLUMN provider_instance_id,
    DROP COLUMN model,
    DROP COLUMN model_options,
    DROP COLUMN runtime_mode,
    DROP COLUMN interaction_mode,
    ADD COLUMN project_id bigint REFERENCES projects (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE snippets
    DROP COLUMN project_id,
    ADD COLUMN provider_instance_id text,
    ADD COLUMN model text,
    ADD COLUMN model_options jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN runtime_mode text NOT NULL DEFAULT 'full-access'
        CHECK (runtime_mode IN ('approval-required', 'auto-accept-edits', 'auto', 'full-access')),
    ADD COLUMN interaction_mode text NOT NULL DEFAULT 'default' CHECK (interaction_mode IN ('default', 'plan'));
ALTER TABLE snippets DROP CONSTRAINT snippets_body_check;
ALTER TABLE snippets ALTER COLUMN body SET DEFAULT '';
ALTER TABLE snippets RENAME COLUMN body TO prompt;
ALTER SEQUENCE snippets_id_seq RENAME TO templates_id_seq;
ALTER TABLE snippets RENAME CONSTRAINT snippets_name_key TO templates_name_key;
ALTER TABLE snippets RENAME CONSTRAINT snippets_pkey TO templates_pkey;
ALTER TABLE snippets RENAME TO templates;
