package train

import "testing"

func TestTravelSourceTrainKind(t *testing.T) {
	for _, tc := range []struct{ number, kind string }{
		{"G123", "high_speed"}, {" g123/4 ", "high_speed"}, {"D88", "emu"}, {"C7", "other"}, {"K1", "other"}, {"1234", "other"}, {"not a train", "unknown"}, {"", "unknown"},
	} {
		if got := sourceTrainKind(tc.number); got != tc.kind {
			t.Fatalf("%q => %q, want %q", tc.number, got, tc.kind)
		}
	}
}
