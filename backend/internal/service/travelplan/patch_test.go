package travelplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agent-platform/backend/internal/evidence"
	v1 "agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeprotocol"
)

const examples = "../../../../openspec/changes/refactor-agent-kernel-plan-execute/contracts/examples"

func loadExample(t *testing.T, name, kind string, out any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(examples, name))
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimeprotocol.DecodeStrict(raw, kind, out); err != nil {
		t.Fatal(err)
	}
}
func snapshotFixture(t *testing.T) v1.PlanSnapshot {
	t.Helper()
	var out v1.PlanSnapshot
	loadExample(t, "05-plan-v1.json", "PlanSnapshot", &out)
	return out
}
func patchFixture(t *testing.T, base v1.PlanSnapshot, raw string) v1.PlanPatch {
	t.Helper()
	var operations []v1.PatchOperation
	if err := json.Unmarshal([]byte(raw), &operations); err != nil {
		t.Fatal(err)
	}
	return v1.PlanPatch{SchemaVersion: 1, PlanID: base.PlanID, ExpectedRevision: base.Revision, Reason: "fixture amendment", Operations: operations}
}

func TestPatchAtomicGraphAndCAS(t *testing.T) {
	base := snapshotFixture(t)
	setup := patchFixture(t, base, `[{"op":"add_stop","value":{"stop_id":"stop_sh","city":"上海"}},{"op":"update_task","task_id":"task_hotel","changes":{"set":{"target":{"kind":"stop","id":"stop_sh"}}}}]`)
	next, err := ProposePatch(base, setup)
	if err != nil {
		t.Fatal(err)
	}
	base = next.Snapshot
	before, _ := json.Marshal(base)
	invalid := patchFixture(t, base, `[{"op":"remove_stop","stop_id":"stop_sh"}]`)
	if _, err = ProposePatch(base, invalid); err == nil {
		t.Fatal("dangling graph committed")
	}
	after, _ := json.Marshal(base)
	if string(after) != string(before) {
		t.Fatal("rejected patch mutated caller snapshot")
	}
	valid := patchFixture(t, base, `[{"op":"remove_stop","stop_id":"stop_sh"},{"op":"update_task","task_id":"task_hotel","changes":{"set":{"target":{"kind":"plan"}}}}]`)
	if _, err = ProposePatch(base, valid); err != nil {
		t.Fatal("same-patch reference repair rejected", err)
	}
	valid.ExpectedRevision--
	if _, err = ProposePatch(base, valid); err == nil {
		t.Fatal("stale expected revision overwrote plan")
	}
	for _, path := range []string{"task_id", "items.0.domain", "/parameters/hotel/city", "parameters.hotel.unknown"} {
		encoded, _ := json.Marshal(map[string]any{"op": "update_task", "task_id": "task_hotel", "changes": map[string]any{"set": map[string]any{path: "forged"}}})
		p := patchFixture(t, base, "["+string(encoded)+"]")
		if _, err = ProposePatch(base, p); err == nil {
			t.Fatalf("protected/arbitrary path accepted: %s", path)
		}
	}
}

func TestTaskDefinitionInvalidatesAllItsItems(t *testing.T) {
	var base v1.PlanSnapshot
	loadExample(t, "26-multicity-plan.json", "PlanSnapshot", &base)
	target := base.Definition.Tasks[0].TaskID
	old := base.TaskVersions[0].TaskRevision
	ops, _ := json.Marshal([]map[string]any{{"op": "update_task", "task_id": target, "changes": map[string]any{"set": map[string]any{"title": "modified task"}}}})
	change, err := ProposePatch(base, patchFixture(t, base, string(ops)))
	if err != nil {
		t.Fatal(err)
	}
	if len(change.InvalidatedTaskIDs) != 1 || change.InvalidatedTaskIDs[0] != target || change.Snapshot.TaskVersions[0].TaskRevision != old+1 {
		t.Fatalf("wrong task invalidation: %+v", change)
	}
	for i := 1; i < len(base.TaskVersions); i++ {
		if base.TaskVersions[i] != change.Snapshot.TaskVersions[i] {
			t.Fatal("unrelated Task version changed")
		}
	}
	noChange, err := ProposePatch(change.Snapshot, patchFixture(t, change.Snapshot, string(ops)))
	if err != nil || noChange.Changed || noChange.Snapshot.Revision != change.Snapshot.Revision || len(noChange.InvalidatedTaskIDs) != 0 {
		t.Fatalf("equal patch produced new revision: %+v %v", noChange, err)
	}
}

func TestSelectionAndRefreshVersions(t *testing.T) {
	base := snapshotFixture(t)
	var selection v1.PlanPatch
	loadExample(t, "27-selection-patch.json", "PlanPatch", &selection)
	selected, err := ProposePatch(base, selection)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Snapshot.Revision != 2 || selected.Snapshot.TaskVersions[0].TaskRevision != 1 || len(selected.InvalidatedTaskIDs) != 0 {
		t.Fatal("selection conflated definition version")
	}
	var refresh v1.PlanPatch
	loadExample(t, "29-explicit-refresh-patch.json", "PlanPatch", &refresh)
	refresh.ExpectedRevision = selected.Snapshot.Revision
	changed, err := ProposePatch(selected.Snapshot, refresh)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Snapshot.Revision != 2 || changed.Snapshot.TaskVersions[0].TaskRevision != 1 || len(changed.RefreshedItemIDs) != 1 || len(changed.InvalidatedTaskIDs) != 0 {
		t.Fatalf("refresh conflated versions: %+v", changed)
	}
}

func TestUnknownPriceAndProvenance(t *testing.T) {
	var definition v1.PlanDefinition
	loadExample(t, "15-minimal-plan-missing-input.json", "PlanDefinition", &definition)
	if err := ValidateDefinition(definition); err != nil {
		t.Fatal("minimal unknown plan rejected", err)
	}
	definition.Requirements[0].Origin = "policy"
	if err := ValidateDefinition(definition); err == nil {
		t.Fatal("user source promoted to policy")
	}
}

func TestPatchUsesOnlySerializedFieldPresence(t *testing.T) {
	base := snapshotFixture(t)
	patch := patchFixture(t, base, `[{"op":"update_task","task_id":"task_hotel","changes":{"set":{"title":"visible"}}}]`)
	// Field.Value is not wire data when Present is false. It must not create an
	// operation that is absent from both the validated request and its hash.
	patch.Operations[0].Changes.Value.Set.Present = false
	if _, err := ProposePatch(base, patch); err == nil {
		t.Fatal("hidden Go field payload changed plan outside serialized request")
	}
}

func TestExecutionInputMatchesProviderRepresentation(t *testing.T) {
	var input map[string]json.RawMessage
	json.Unmarshal([]byte(`{"origin":"北京南","destination":"上海虹桥","departure_date":"2026-11-10","passengers":1,"keywords":["高铁","二等座"]}`), &input)
	matching := json.RawMessage(`{"domain":"train","origin":"北京南","destination":"上海虹桥","date":"2026-11-10","passengers":1,"keywords":"高铁 二等座"}`)
	if !ArgumentsMatch("train", input, matching) {
		t.Fatal("equivalent registered provider parameters rejected")
	}
	for _, different := range []string{`{"domain":"hotel","origin":"北京南","destination":"上海虹桥","date":"2026-11-10","passengers":1,"keywords":"高铁 二等座"}`, `{"domain":"train","origin":"北京南","destination":"上海虹桥","date":"2026-11-11","passengers":1,"keywords":"高铁 二等座"}`} {
		if ArgumentsMatch("train", input, json.RawMessage(different)) {
			t.Fatal("different actual query inputs reused")
		}
	}
}

func TestExecutionInputPreservesNullAndUnknownFields(t *testing.T) {
	var input map[string]json.RawMessage
	json.Unmarshal([]byte(`{"city":"上海","check_in":"2026-11-10","check_out":"2026-11-12","guests":1}`), &input)
	if ArgumentsMatch("hotel", input, json.RawMessage(`{"city":"上海","check_in":"2026-11-10","check_out":"2026-11-12","guests":1,"keywords":null}`)) {
		t.Fatal("explicit null treated as absent query input")
	}
	web := map[string]json.RawMessage{"query": json.RawMessage(`"hotel"`)}
	if ArgumentsMatch("web", web, json.RawMessage(`{"query":"hotel","keywords":""}`)) {
		t.Fatal("unknown provider field silently ignored")
	}
}

func TestExecutionInputWeatherFactsReachResult(t *testing.T) {
	expiry := "2026-11-11T00:00:00Z"
	record := v1.SharedToolEvidenceRecord{ID: "weather-fact", ResultRevision: 1, Domain: "weather", Mode: "live", Provider: "fixture", FetchedAt: "2026-11-08T00:00:00Z", ExpiresAt: &expiry, Snapshot: map[string]json.RawMessage{"query_context": json.RawMessage(`{"domain":"weather","mode":"live","provider":"fixture","arguments":{"city":"上海","date":"2026-11-10"},"input_hash":"fixture","coverage":"complete"}`), "candidates": json.RawMessage(`[]`), "result": json.RawMessage(`{"weather":[{"date":"2026-11-10","description":"晴","temperature_c":18}],"source":{"provider":"fixture"}}`)}}
	request := v1.ExecutionRequest{ExecutionStepID: "step", PlanID: "plan", TaskID: "task", TaskRevision: 1, ItemID: "item", Generation: 1, Domain: "weather"}
	result, err := EvidenceResult(request, evidence.View{ReferenceID: record.ID, ResultRevision: 1, Record: record})
	if err != nil || len(result.Candidates) != 1 || string(result.Candidates[0].Attributes["temperature_c"]) != "18" {
		t.Fatalf("weather facts lost from replanner result: %+v %v", result, err)
	}
}
