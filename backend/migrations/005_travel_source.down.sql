-- Batch operations may contain multiple keys. They cannot be represented by 004.
DO $$ BEGIN RAISE EXCEPTION '005 travel memory batches require an explicit data migration before downgrade'; END $$;
