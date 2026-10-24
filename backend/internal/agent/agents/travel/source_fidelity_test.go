package travel

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

// These are hashes of evaluated Python strings, including concatenation in
// MAIN_PROMPT, not hashes of source lines or approximate prompt fragments.
func TestTravelSourcePrompts(t *testing.T) {
	for _, tc := range []struct{ name, digest string }{
		{"main", "56bdc5492e5b6d2aea654ff51a523cbe50ae4db7709927808562c65c117dc879"},
		{"review", "d45fd174effd9dece084ff3ad70386dedb8fc992ec7ff362983afb350bebb1e9"},
		{"summary", "1c3132cef7abd569d6486c0c060498b51c5e15db92023c99fabd50b84528ae75"},
		{"reply", "acafaef5a607227e9aa9601b9757e12eeea232228d95fce3be373da868966dbc"},
		{"rail", "a992e27c6879a9cdce404e309576ab082b494d4bf5ae232f1a3eab3330890a55"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data []byte
			if tc.name == "main" {
				data = []byte(instruction)
			} else {
				data = []byte(map[string]string{"review": reviewInstruction, "summary": summaryInstruction, "reply": replyPolicy, "rail": railPlanningPolicy}[tc.name])
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != tc.digest {
				t.Fatalf("original %s prompt changed: SHA256=%s, want %s", tc.name, got, tc.digest)
			}
		})
	}
	aux, err := os.ReadFile("testdata/source_auxiliary_prompts.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(aux)); got != "b33979342d45d35801dec92d464881174468946681527a293b8fca68374bfa71" {
		t.Fatalf("original auxiliary prompt fixture changed: %s", got)
	}
}
