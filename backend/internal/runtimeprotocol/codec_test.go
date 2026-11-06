package runtimeprotocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	v1 "agent-platform/backend/internal/model/runtimev1"
)

const contracts = "../../../openspec/changes/refactor-agent-kernel-plan-execute/contracts"

func fixture(t *testing.T, file string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(contracts, "examples", file))
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func dto(definition string) any {
	switch definition {
	case "TravelToolArgs":
		return &v1.TravelToolArgs{}
	case "DelegationInput":
		return &v1.DelegationInput{}
	case "PlannerInput":
		return &v1.PlannerInput{}
	case "PlanDefinition":
		return &v1.PlanDefinition{}
	case "PlanPatch":
		return &v1.PlanPatch{}
	case "PlanSnapshot":
		return &v1.PlanSnapshot{}
	case "ExecutionRequest":
		return &v1.ExecutionRequest{}
	case "ExecutionResult":
		return &v1.ExecutionResult{}
	case "ReplannerInput":
		return &v1.ReplannerInput{}
	case "ReplannerDecision":
		return &v1.ReplannerDecision{}
	case "PlanView":
		return &v1.PlanView{}
	case "CommitResult":
		return &v1.CommitResult{}
	case "TravelCallResult":
		return &v1.TravelCallResult{}
	case "MemoryReviewDecision":
		return &v1.MemoryReviewDecision{}
	case "MemorySubmitArgs":
		return &v1.MemorySubmitArgs{}
	case "MemoryTaskReceipt":
		return &v1.MemoryTaskReceipt{}
	case "MemoryAgentInput":
		return &v1.MemoryAgentInput{}
	case "MemoryProposal":
		return &v1.MemoryProposal{}
	case "MemoryMaintenanceResult":
		return &v1.MemoryMaintenanceResult{}
	case "GeneralContext":
		return &v1.GeneralContext{}
	case "MemoryWriteArgs":
		return &v1.MemoryWriteArgs{}
	case "MemoryWriteResult":
		return &v1.MemoryWriteResult{}
	case "UserMemorySnapshot":
		return &v1.UserMemorySnapshot{}
	case "SharedToolCallRecord":
		return &v1.SharedToolCallRecord{}
	case "SharedToolEvidenceRecord":
		return &v1.SharedToolEvidenceRecord{}
	case "DispatchCommand":
		return &v1.DispatchCommand{}
	case "TravelTaskSubmit":
		return &v1.TravelTaskSubmit{}
	case "TrustedTravelMeta":
		return &v1.TrustedTravelMeta{}
	case "TravelTaskReceipt":
		return &v1.TravelTaskReceipt{}
	case "TravelTaskResult":
		return &v1.TravelTaskResult{}
	case "TravelTaskFind":
		return &v1.TravelTaskFind{}
	case "TravelTaskLookup":
		return &v1.TravelTaskLookup{}
	default:
		return nil
	}
}

func jsonValue(t *testing.T, b []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value any
	if e := d.Decode(&value); e != nil {
		t.Fatal(e)
	}
	return value
}

func TestWireFixtureParity(t *testing.T) {
	var manifest struct {
		Examples []struct{ File, Definition string }
	}
	b, e := os.ReadFile(filepath.Join(contracts, "examples/manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &manifest); e != nil {
		t.Fatal(e)
	}
	if len(manifest.Examples) != 43 {
		t.Fatal("fixture manifest changed: review coverage")
	}
	for _, entry := range manifest.Examples {
		t.Run(entry.File, func(t *testing.T) {
			raw := fixture(t, entry.File)
			value := dto(entry.Definition)
			if value == nil {
				t.Fatalf("no typed DTO for %s", entry.Definition)
			}
			if e := DecodeStrict(raw, entry.Definition, value); e != nil {
				t.Fatal(e)
			}
			encoded, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(jsonValue(t, raw), jsonValue(t, encoded)) {
				t.Fatalf("DTO lost or invented fields: %s", encoded)
			}
		})
	}
}

func TestJCSCanonicalInput(t *testing.T) {
	var cases []struct{ Input, Want string }
	b, e := os.ReadFile("testdata/jcs-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, tc := range cases {
		got, e := canonicalInput([]byte(tc.Input))
		if e != nil {
			t.Fatal(e)
		}
		if string(got) != tc.Want {
			t.Fatalf("got %s want %s", got, tc.Want)
		}
	}
	var submit v1.TravelTaskSubmit
	var meta v1.TrustedTravelMeta
	if e = DecodeStrict(fixture(t, "38-travel-submit.json"), "TravelTaskSubmit", &submit); e != nil {
		t.Fatal(e)
	}
	if e = DecodeStrict(fixture(t, "39-trusted-travel-meta.json"), "TrustedTravelMeta", &meta); e != nil {
		t.Fatal(e)
	}
	args, e := json.Marshal(submit.Args)
	if e != nil {
		t.Fatal(e)
	}
	hash, e := InputHash(args, meta)
	if e != nil {
		t.Fatal(e)
	}
	if hash != submit.InputHash {
		t.Fatalf("hash differs from approved fixture: %s", hash)
	}
	var reversed map[string]json.RawMessage
	if e = json.Unmarshal(args, &reversed); e != nil {
		t.Fatal(e)
	}
	again, e := json.MarshalIndent(reversed, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	hash2, e := InputHash(again, meta)
	if e != nil || hash2 != hash {
		t.Fatal("whitespace/key ordering changed identity")
	}
}

func TestRejectLossyJSONBeforeCanonicalization(t *testing.T) {
	for _, raw := range []string{`{"a":1,"\u0061":2}`, `{"n":9007199254740993}`, `{"n":9007199254740993e0}`, `{"n":1e309}`, `{"n":1e-400}`, `{"s":"\ud800"}`, `{"s":"\udfff"}`, `{} {}`, `[1,]`} {
		if _, e := canonicalInput([]byte(raw)); e == nil {
			t.Fatalf("lossy/ambiguous JSON accepted: %s", raw)
		}
	}
	if _, e := canonicalInput([]byte{'"', 0xff, '"'}); e == nil {
		t.Fatal("invalid UTF8 accepted")
	}
}

func TestRejectSpoofedScopeAndAcceptedAsResult(t *testing.T) {
	args := jsonValue(t, fixture(t, "01-travel-tool-create.json")).(map[string]any)
	args["subject_id"] = "untrusted"
	raw, _ := json.Marshal(args)
	if e := DecodeStrict(raw, "TravelToolArgs", &v1.TravelToolArgs{}); e == nil {
		t.Fatal("model injected trusted identity")
	}
	if e := DecodeStrict(fixture(t, "40-travel-task-receipt.json"), "TravelTaskResult", &v1.TravelTaskResult{}); e == nil {
		t.Fatal("receipt accepted as final result")
	}
	var receipt v1.TravelTaskReceipt
	var result v1.TravelTaskResult
	if e := DecodeStrict(fixture(t, "40-travel-task-receipt.json"), "TravelTaskReceipt", &receipt); e != nil {
		t.Fatal(e)
	}
	if e := DecodeStrict(fixture(t, "41-travel-task-result.json"), "TravelTaskResult", &result); e != nil {
		t.Fatal(e)
	}
	if e := ValidateResult(receipt, result); e != nil {
		t.Fatal(e)
	}
	result.RequestKey = "another-key"
	if e := ValidateResult(receipt, result); e == nil {
		t.Fatal("foreign result accepted")
	}
	if e := DecodeStrict(fixture(t, "42-travel-task-failed.json"), "TravelTaskResult", &result); e != nil {
		t.Fatal(e)
	}
	if e := ValidateResult(receipt, result); e != nil {
		t.Fatal("true failed partial result must remain collectable: ", e)
	}
	result.Result.CallStatus = "succeeded"
	if e := ValidateResult(receipt, result); e == nil {
		t.Fatal("failed task presented as success")
	}
}

func TestSchemaDoesNotRoundFractionalIntegers(t *testing.T) {
	for _, raw := range []string{`{"kind":"message","id":"m","revision":1.00000000000000000001}`, `{"kind":"message","id":"m","revision":0.99999999999999999999}`} {
		if err := DecodeStrict([]byte(raw), "SourceRef", &v1.SourceRef{}); err == nil {
			t.Fatal("fractional revision rounded to an integer before validation")
		}
	}
}

func TestOptionalNullAndMissingRemainDistinct(t *testing.T) {
	for _, raw := range []string{`{"stop_id":"s","city":"杭州"}`, `{"stop_id":"s","city":"杭州","arrival_date":null}`, `{"stop_id":"s","city":"杭州","arrival_date":"2026-11-07"}`} {
		var stop v1.Stop
		if e := DecodeStrict([]byte(raw), "Stop", &stop); e != nil {
			t.Fatal(e)
		}
		encoded, e := json.Marshal(stop)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(jsonValue(t, []byte(raw)), jsonValue(t, encoded)) {
			t.Fatal("missing/null/value collapsed")
		}
	}
}

func TestStrictFormatsAndUnknownDefinitions(t *testing.T) {
	for _, raw := range []string{`{"stop_id":"s","city":"杭州","arrival_date":"2026-02-30"}`, `{"stop_id":"s","city":"杭州","arrival_date":"20261107"}`} {
		if e := DecodeStrict([]byte(raw), "Stop", &v1.Stop{}); e == nil {
			t.Fatal("invalid date accepted")
		}
	}
	if e := DecodeStrict([]byte(`{}`), "Unknown", &map[string]any{}); e == nil {
		t.Fatal("undefined schema accepted")
	}
	if e := DecodeStrict([]byte(strings.Repeat(" ", 1000001)), "Stop", &v1.Stop{}); e == nil {
		t.Fatal("oversized wire accepted")
	}
}

func TestDecodeDoesNotCarryFieldsFromPreviousDocument(t *testing.T) {
	var stop v1.Stop
	if err := DecodeStrict([]byte(`{"stop_id":"s","city":"杭州","arrival_date":"2026-11-07"}`), "Stop", &stop); err != nil {
		t.Fatal(err)
	}
	if err := DecodeStrict([]byte(`{"stop_id":"s2","city":"北京"}`), "Stop", &stop); err != nil {
		t.Fatal(err)
	}
	if stop.ArrivalDate.Present {
		t.Fatal("previous document field leaked into new document")
	}
	if err := DecodeStrict([]byte(`{"stop_id":"bad","city":""}`), "Stop", &stop); err == nil {
		t.Fatal("invalid document accepted")
	}
	if stop.StopID != "s2" {
		t.Fatal("failed decode modified the destination")
	}
}

func TestDeployedSchemaMatchesFormalSource(t *testing.T) {
	formal, err := os.ReadFile(filepath.Join(contracts, "agent-contracts.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(formal, schemaBytes) {
		t.Fatal("deployed schema diverged from sole formal specification")
	}
}

func TestEquivalentIntegerNotationInWire(t *testing.T) {
	for _, raw := range []string{`{"kind":"message","id":"msg","revision":1.0}`, `{"kind":"message","id":"msg","revision":1e0}`} {
		var source v1.SourceRef
		if err := DecodeStrict([]byte(raw), "SourceRef", &source); err != nil {
			t.Fatal(err)
		}
		if source.Revision.Value != 1 || !source.Revision.Present {
			t.Fatal("integer notation changed the revision")
		}
	}
}

func TestStrictTimestampOffset(t *testing.T) {
	for _, timestamp := range []string{"2026-11-07T00:00:00+24:00", "2026-11-07T00:00:00+01:60", "2026-11-07 00:00:00Z", "2026-02-30T00:00:00Z"} {
		value := jsonValue(t, fixture(t, "39-trusted-travel-meta.json")).(map[string]any)
		value["deadline_at"] = timestamp
		raw, _ := json.Marshal(value)
		if err := DecodeStrict(raw, "TrustedTravelMeta", &v1.TrustedTravelMeta{}); err == nil {
			t.Fatalf("invalid timestamp accepted: %s", timestamp)
		}
	}
}

func TestStrictSchemaSafety(t *testing.T) {
	cases := []struct {
		file, definition string
		path             []string
		value            any
	}{
		{"37-runtime-dispatch.json", "DispatchCommand", []string{"dispatch_seq"}, 0},
		{"37-runtime-dispatch.json", "DispatchCommand", []string{"api_key"}, "untrusted"},
		{"38-travel-submit.json", "TravelTaskSubmit", []string{"hash_version"}, "unversioned"},
		{"38-travel-submit.json", "TravelTaskSubmit", []string{"input_hash"}, "not-a-hash"},
		{"38-travel-submit.json", "TravelTaskSubmit", []string{"args", "scope_id"}, "untrusted"},
		{"39-trusted-travel-meta.json", "TrustedTravelMeta", []string{"source_context_epoch"}, -1},
		{"40-travel-task-receipt.json", "TravelTaskReceipt", []string{"status"}, "accepted"},
		{"42-travel-task-failed.json", "TravelTaskResult", []string{"result", "error"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.definition+"/"+strings.Join(tc.path, "."), func(t *testing.T) {
			root := jsonValue(t, fixture(t, tc.file)).(map[string]any)
			current := root
			for _, key := range tc.path[:len(tc.path)-1] {
				current = current[key].(map[string]any)
			}
			current[tc.path[len(tc.path)-1]] = tc.value
			raw, _ := json.Marshal(root)
			if err := DecodeStrict(raw, tc.definition, dto(tc.definition)); err == nil {
				t.Fatal("unsafe schema value accepted")
			}
		})
	}
}
