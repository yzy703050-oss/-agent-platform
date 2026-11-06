package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func runtimeEnvironment() map[string]string {
	return map[string]string{
		"RUNTIME_ROLE": "worker", "RUNTIME_INSTANCE_ID": "worker-test-1",
		"RUNTIME_PLATFORM_DSN":       "postgres://platform_runtime:private-password@localhost/test?sslmode=disable",
		"RUNTIME_REDIS_URL":          "redis://localhost:6379/0",
		"RUNTIME_KAFKA_BROKERS":      "localhost:9092",
		"RUNTIME_TRAVEL_MCP_URL":     "http://127.0.0.1:8090/mcp",
		"RUNTIME_RAG_MCP_URL":        "http://127.0.0.1:8091/mcp",
		"RUNTIME_MAX_MODEL_ATTEMPTS": "32", "RUNTIME_MAX_TOOL_ATTEMPTS": "64",
		"RUNTIME_MAX_TOKEN_BUDGET": "500000", "RUNTIME_MODEL_INPUT_TOKENS": "20000",
		"RUNTIME_MODEL_OUTPUT_TOKENS": "4096", "RUNTIME_RETURN_RESERVE_SECONDS": "15",
		"RUNTIME_RESOURCE_LIMITS": `{"model:fixture":2,"rag:fixture":2}`,
	}
}

func TestRuntimeConfigRequiresFiniteBudgets(t *testing.T) {
	// Missing/overflowing limits must fail admission instead of implying infinity.
	for _, key := range []string{"RUNTIME_MAX_MODEL_ATTEMPTS", "RUNTIME_MAX_TOOL_ATTEMPTS", "RUNTIME_MAX_TOKEN_BUDGET", "RUNTIME_MODEL_INPUT_TOKENS", "RUNTIME_MODEL_OUTPUT_TOKENS", "RUNTIME_RETURN_RESERVE_SECONDS", "RUNTIME_RESOURCE_LIMITS"} {
		for _, value := range []string{"", "0", "-1", "9223372036854775808"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				env := runtimeEnvironment()
				env[key] = value
				if _, err := LoadRuntime(func(k string) string { return env[k] }); err == nil {
					t.Fatal("invalid or missing finite limit accepted")
				}
			})
		}
	}
	for _, value := range []string{`{}`, `{"model:fixture":0}`, `{"model:fixture":-1}`, `{"model:fixture":1.5}`, `null`} {
		env := runtimeEnvironment()
		env["RUNTIME_RESOURCE_LIMITS"] = value
		if _, err := LoadRuntime(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("invalid resources accepted: %s", value)
		}
	}
}

func TestRuntimeConfigSeparatesRoles(t *testing.T) {
	for _, role := range []string{"api", "worker", "travel", "maintenance", "migrate"} {
		t.Run(role, func(t *testing.T) {
			env := runtimeEnvironment()
			env["RUNTIME_ROLE"] = role
			env["RUNTIME_COMPONENT"] = "control"
			env["RUNTIME_TRAVEL_DSN"] = "postgres://travel_runtime:travel-secret@localhost/test"
			env["RUNTIME_CONTROL_DSN"] = "postgres://control_migrator:control-secret@localhost/test"
			read := map[string]bool{}
			cfg, err := LoadRuntime(func(k string) string { read[k] = true; return env[k] })
			if err != nil {
				t.Fatal(err)
			}
			want := "RUNTIME_PLATFORM_DSN"
			if role == "travel" {
				want = "RUNTIME_TRAVEL_DSN"
			}
			if role == "migrate" {
				want = "RUNTIME_CONTROL_DSN"
			}
			for _, key := range []string{"RUNTIME_PLATFORM_DSN", "RUNTIME_TRAVEL_DSN", "RUNTIME_CONTROL_DSN"} {
				if read[key] != (key == want) {
					t.Fatalf("role %s unexpectedly accesses credential %s", role, key)
				}
			}
			encoded, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"private-password", "travel-secret", "control-secret"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("configuration JSON leaks credential")
				}
			}
		})
	}
	env := runtimeEnvironment()
	env["RUNTIME_ROLE"] = "unknown"
	if _, err := LoadRuntime(func(k string) string { return env[k] }); err == nil {
		t.Fatal("unknown role accepted")
	}
}

func TestRuntimeConfigPreservesAbsoluteLimits(t *testing.T) {
	env := runtimeEnvironment()
	cfg, err := LoadRuntime(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RunSeconds != 300 || cfg.ToolSeconds != 10 || cfg.HeartbeatSeconds != 5 || cfg.LeaseSeconds != 20 {
		t.Fatalf("unexpected time defaults: %+v", cfg)
	}
	if cfg.EntrySlots != 2 || cfg.DispatchSlots != 2 || cfg.TravelSlots != 1 || cfg.PGPoolMax != 4 {
		t.Fatal("unexpected capacity defaults")
	}
	if cfg.OutboxPollMillis != 1000 || cfg.OutboxBatch != 100 || cfg.OutboxLeaseSeconds != 30 || cfg.SweepMillis != 1000 {
		t.Fatal("unexpected polling defaults")
	}
	for key, value := range map[string]string{"RUNTIME_RUN_SECONDS": "60", "RUNTIME_TOOL_SECONDS": "3", "RUNTIME_PG_POOL_MAX": "2", "RUNTIME_OUTBOX_BATCH": "5"} {
		env[key] = value
	}
	cfg, err = LoadRuntime(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RunSeconds != 60 || cfg.ToolSeconds != 3 || cfg.PGPoolMax != 2 || cfg.OutboxBatch != 5 || cfg.MaxTokenBudget != 500000 {
		t.Fatal("explicit limits were reset")
	}
	for _, pair := range [][2]string{{"RUNTIME_RETURN_RESERVE_SECONDS", "60"}, {"RUNTIME_LEASE_SECONDS", "5"}, {"RUNTIME_PG_POOL_MAX", "5"}, {"RUNTIME_TOOL_SECONDS", "60"}, {"RUNTIME_ENTRY_SLOTS", "0"}, {"RUNTIME_RUN_SECONDS", "bad"}} {
		bad := runtimeEnvironment()
		bad["RUNTIME_RUN_SECONDS"] = "60"
		bad[pair[0]] = pair[1]
		if _, err := LoadRuntime(func(k string) string { return bad[k] }); err == nil {
			t.Fatalf("invalid relationship %s accepted", pair[0])
		}
	}
}

func TestRuntimeConfigRejectsUnsafeEndpointsWithoutSecrets(t *testing.T) {
	for _, pair := range [][2]string{{"RUNTIME_PLATFORM_DSN", "postgres://user:never-print@/db"}, {"RUNTIME_REDIS_URL", "http://user:never-print@remote/"}, {"RUNTIME_RAG_MCP_URL", "http://remote.example/mcp"}, {"RUNTIME_TRAVEL_MCP_URL", "https://user:never-print@remote/mcp"}, {"RUNTIME_INSTANCE_ID", ""}} {
		env := runtimeEnvironment()
		env[pair[0]] = pair[1]
		_, err := LoadRuntime(func(k string) string { return env[k] })
		if err == nil {
			t.Fatalf("unsafe %s accepted", pair[0])
		}
		if strings.Contains(err.Error(), "never-print") {
			t.Fatal("validation error leaks credential")
		}
	}
}

func TestRuntimeConfigCannotReserveOverflowingModelTokens(t *testing.T) {
	for _, values := range [][3]string{{"100", "70", "31"}, {"9223372036854775807", "9223372036854775807", "1"}} {
		env := runtimeEnvironment()
		env["RUNTIME_MAX_TOKEN_BUDGET"], env["RUNTIME_MODEL_INPUT_TOKENS"], env["RUNTIME_MODEL_OUTPUT_TOKENS"] = values[0], values[1], values[2]
		if _, err := LoadRuntime(func(k string) string { return env[k] }); err == nil {
			t.Fatal("model reservation exceeds aggregate budget or overflows")
		}
	}
}
