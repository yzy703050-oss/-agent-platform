package evidence

import (
	"strings"
	"testing"
)

func TestRemoteIdentityBoundAndNamespaces(t *testing.T) {
	original := strings.Repeat("原始/标识", 1000)
	id, err := RemoteID(strings.Repeat("a", 32), original)
	if err != nil || len(id) > 160 || !strings.HasPrefix(id, "remote:") {
		t.Fatalf("identity not bounded: %d %v", len(id), err)
	}
	repeat, _ := RemoteID(strings.Repeat("a", 32), original)
	other, _ := RemoteID("travel", original)
	if repeat != id || other == id {
		t.Fatal("identity namespace unstable")
	}
	for _, slug := range []string{"", strings.Repeat("a", 33), "Platform", "travel:x", "../travel"} {
		if _, err := RemoteID(slug, original); err == nil {
			t.Fatalf("unregistered slug accepted: %q", slug)
		}
	}
}
