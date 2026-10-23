package mcp

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPolicyProtocol(t *testing.T) {
	for _, tt := range []struct {
		name, payload string
		valid         bool
	}{
		{"hit", `{"schema_version":1,"status":"ok","collection":"policy","fragments":[{"id":"c1","chunk_id":"c1","content":"真实条文","metadata":{"source":"policy.pdf","region":"上海"}}]}`, true},
		{"miss", `{"schema_version":1,"status":"no_knowledge","collection":"policy","fragments":[]}`, true},
		{"forged_source", `{"schema_version":1,"status":"ok","collection":"policy","fragments":[{"id":"c1","chunk_id":"c1","content":"无来源","metadata":{}}]}`, false},
		{"wrong_collection", `{"schema_version":1,"status":"no_knowledge","collection":"other","fragments":[]}`, false},
		{"unsupported_version", `{"schema_version":2,"status":"no_knowledge","collection":"policy","fragments":[]}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodePolicy([]byte(tt.payload), "policy")
			if (err == nil) != tt.valid {
				t.Fatalf("protocol validity=%v: %v", tt.valid, err)
			}
		})
	}
}

// Opt-in smoke without importing or modifying knowledge documents.
func TestPolicyMCPReal(t *testing.T) {
	command, dir, collection := os.Getenv("RAG_MCP_COMMAND"), os.Getenv("RAG_MCP_DIR"), os.Getenv("RAG_COLLECTION")
	if os.Getenv("REQUIRE_RAG_TEST") != "1" {
		t.Skip("opt-in real MCP smoke")
	}
	if command == "" || dir == "" || collection == "" {
		t.Fatal("RAG configuration required")
	}
	c, err := New(StdioConfig{Command: command, Directory: dir, Collection: collection, Args: []string{"-m", "src.mcp_server.server"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, err := c.Search(ctx, "差旅住宿报销标准")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("real MCP: collection=%s status=%s fragments=%d", result.Collection, result.Status, len(result.Fragments))
}
