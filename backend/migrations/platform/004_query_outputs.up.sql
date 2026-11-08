ALTER TABLE platform.memory_operations ADD CONSTRAINT operation_message_owner UNIQUE(id,user_id,conversation_id,thread_id);
ALTER TABLE platform.messages ALTER COLUMN run_id DROP NOT NULL;
ALTER TABLE platform.messages ADD COLUMN operation_id text;
ALTER TABLE platform.messages ADD CONSTRAINT message_execution_owner CHECK(
 (run_id IS NOT NULL AND operation_id IS NULL) OR
 (run_id IS NULL AND operation_id IS NOT NULL AND message_kind='model_output'));
ALTER TABLE platform.messages ADD CONSTRAINT message_operation_owner FOREIGN KEY(operation_id,user_id,conversation_id,thread_id)
 REFERENCES platform.memory_operations(id,user_id,conversation_id,thread_id);
CREATE UNIQUE INDEX operation_model_output ON platform.messages(operation_id,thread_id,source_key) WHERE operation_id IS NOT NULL;
CREATE TRIGGER immutable_model_output BEFORE UPDATE OR DELETE ON platform.messages FOR EACH ROW WHEN(OLD.message_kind='model_output') EXECUTE FUNCTION platform.reject_fact_mutation();
