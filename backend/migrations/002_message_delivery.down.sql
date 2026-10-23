DROP INDEX IF EXISTS message_source_key;
ALTER TABLE messages DROP COLUMN source_key;
ALTER TABLE messages DROP COLUMN attempt;
