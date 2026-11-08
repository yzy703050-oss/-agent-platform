CREATE TRIGGER immutable_model_output BEFORE UPDATE OR DELETE ON travel.messages FOR EACH ROW WHEN(OLD.message_kind='model_output') EXECUTE FUNCTION travel.reject_fact_mutation();
