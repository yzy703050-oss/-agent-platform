ALTER TABLE messages ADD COLUMN attempt bigint NOT NULL DEFAULT 1;
ALTER TABLE messages ADD COLUMN source_key text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX message_source_key ON messages(run_id,source_key) WHERE source_key<>'';
