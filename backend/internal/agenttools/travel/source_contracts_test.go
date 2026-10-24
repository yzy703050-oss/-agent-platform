package travel

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestTravelSourceContracts(t *testing.T) {
	data, err := os.ReadFile("testdata/source_contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != "656952580305329a6d32563888185a3113240e2140e1c34be3fab7680e817185" {
		t.Fatalf("frozen Python schema fixture changed: %s", got)
	}
	var expected map[string][]struct {
		Function struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Parameters  any    `json:"parameters"`
		} `json:"function"`
	}
	if err = json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	for profile, defs := range expected {
		t.Run(profile, func(t *testing.T) {
			infos, err := SourceProfile(profile)
			if err != nil {
				t.Fatal(err)
			}
			if len(infos) != len(defs) {
				t.Fatalf("tool count=%d want %d", len(infos), len(defs))
			}
			for i, info := range infos {
				want := defs[i].Function
				if info.Name != want.Name || info.Desc != want.Description {
					t.Fatalf("tool instruction changed: %s", info.Name)
				}
				params, err := info.ParamsOneOf.ToJSONSchema()
				if err != nil {
					t.Fatal(err)
				}
				b, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				var actual any
				if err = json.Unmarshal(b, &actual); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual, want.Parameters) {
					t.Fatalf("source parameter schema changed for %s/%s", profile, info.Name)
				}
			}
		})
	}
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(TripArgumentHelp))); got != "8f3a64bf04bafeb97ef733a2b79858009465f754e6a7c6b09abb0d943038ccf0" {
		t.Fatalf("original TRIP_ARGUMENT_HELP changed: %s", got)
	}
}

func TestTravelNativePatchPresence(t *testing.T) {
	if err := strictDecode([]byte(`{} invalid-trailing-data`), new(NativeReadArguments)); err == nil {
		t.Fatal("trailing malformed tool arguments accepted")
	}
	for _, tc := range []struct {
		input          string
		present, empty bool
	}{
		{`{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":2,"tasks":[{"task_id":"t"}]}}`, false, false},
		{`{"request_kind":"planning","tripplan":{"operation":"amend","trip_plan_id":"p","expected_revision":2,"tasks":[{"task_id":"t","content":[]}]}}`, true, true},
	} {
		var args NativeTripArguments
		if err := json.Unmarshal([]byte(tc.input), &args); err != nil {
			t.Fatal(err)
		}
		task := args.Tripplan.Tasks[0]
		if task.Has("content") != tc.present {
			t.Fatal("lost explicit patch presence")
		}
		if tc.empty && (task.Content == nil || len(*task.Content) != 0) {
			t.Fatal("explicit empty content was lost")
		}
	}
	var args NativeTripArguments
	if json.Unmarshal([]byte(`{"request_kind":"planning","tripplan":{"operation":"select","expected_revision":1.5}}`), &args) == nil {
		t.Fatal("non-integer business revision accepted")
	}
}
