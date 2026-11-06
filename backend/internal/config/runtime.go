package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// RuntimeConfig is loaded once per runtime-v1 process. Run admission copies its
// limits into persistent state; recovery must not reload limits over that state.
type RuntimeConfig struct {
	Role, InstanceID, Component                                    string
	PlatformDSN                                                    string `json:"-"`
	TravelDSN                                                      string `json:"-"`
	ControlDSN                                                     string `json:"-"`
	RedisURL                                                       string `json:"-"`
	KafkaBrokers                                                   []string
	TravelMCPURL, RAGMCPURL                                        string
	RunSeconds, ToolSeconds, HeartbeatSeconds, LeaseSeconds        int
	ReturnReserveSeconds                                           int
	EntrySlots, DispatchSlots, TravelSlots, PGPoolMax              int
	OutboxPollMillis, OutboxBatch, OutboxLeaseSeconds, SweepMillis int
	MaxModelAttempts, MaxToolAttempts                              int
	MaxTokenBudget, ModelInputTokens, ModelOutputTokens            int64
	ResourceLimits                                                 map[string]int
}

func (c RuntimeConfig) String() string   { b, _ := json.Marshal(c); return string(b) }
func (c RuntimeConfig) GoString() string { return c.String() }

func LoadRuntime(get func(string) string) (RuntimeConfig, error) {
	var c RuntimeConfig
	fail := func(key string) (RuntimeConfig, error) {
		return RuntimeConfig{}, fmt.Errorf("invalid or missing %s", key)
	}
	c.Role, c.InstanceID = get("RUNTIME_ROLE"), get("RUNTIME_INSTANCE_ID")
	if strings.TrimSpace(c.InstanceID) == "" || len(c.InstanceID) > 128 {
		return fail("RUNTIME_INSTANCE_ID")
	}
	var dsnKey string
	switch c.Role {
	case "api", "worker", "maintenance":
		dsnKey = "RUNTIME_PLATFORM_DSN"
	case "travel":
		dsnKey = "RUNTIME_TRAVEL_DSN"
	case "migrate":
		c.Component = get("RUNTIME_COMPONENT")
		switch c.Component {
		case "platform":
			dsnKey = "RUNTIME_PLATFORM_DSN"
		case "travel":
			dsnKey = "RUNTIME_TRAVEL_DSN"
		case "control":
			dsnKey = "RUNTIME_CONTROL_DSN"
		default:
			return fail("RUNTIME_COMPONENT")
		}
	default:
		return fail("RUNTIME_ROLE")
	}
	dsn := get(dsnKey)
	if !runtimeURL(dsn, "postgres", "postgresql") {
		return fail(dsnKey)
	}
	switch dsnKey {
	case "RUNTIME_PLATFORM_DSN":
		c.PlatformDSN = dsn
	case "RUNTIME_TRAVEL_DSN":
		c.TravelDSN = dsn
	case "RUNTIME_CONTROL_DSN":
		c.ControlDSN = dsn
	}
	if c.Role != "migrate" {
		c.RedisURL = get("RUNTIME_REDIS_URL")
		if !runtimeURL(c.RedisURL, "redis", "rediss") {
			return fail("RUNTIME_REDIS_URL")
		}
	}
	if c.Role == "worker" {
		for _, address := range strings.Split(get("RUNTIME_KAFKA_BROKERS"), ",") {
			address = strings.TrimSpace(address)
			host, port, err := net.SplitHostPort(address)
			p, e := strconv.Atoi(port)
			if err != nil || host == "" || e != nil || p < 1 || p > 65535 {
				return fail("RUNTIME_KAFKA_BROKERS")
			}
			c.KafkaBrokers = append(c.KafkaBrokers, address)
		}
		c.TravelMCPURL = get("RUNTIME_TRAVEL_MCP_URL")
		if !runtimeMCPURL(c.TravelMCPURL) {
			return fail("RUNTIME_TRAVEL_MCP_URL")
		}
	}
	if c.Role == "worker" || c.Role == "travel" {
		c.RAGMCPURL = get("RUNTIME_RAG_MCP_URL")
		if !runtimeMCPURL(c.RAGMCPURL) {
			return fail("RUNTIME_RAG_MCP_URL")
		}
	}
	ints := []struct {
		key      string
		dst      *int
		fallback int
	}{
		{"RUNTIME_RUN_SECONDS", &c.RunSeconds, 300}, {"RUNTIME_TOOL_SECONDS", &c.ToolSeconds, 10},
		{"RUNTIME_HEARTBEAT_SECONDS", &c.HeartbeatSeconds, 5}, {"RUNTIME_LEASE_SECONDS", &c.LeaseSeconds, 20},
		{"RUNTIME_RETURN_RESERVE_SECONDS", &c.ReturnReserveSeconds, 0},
		{"RUNTIME_ENTRY_SLOTS", &c.EntrySlots, 2}, {"RUNTIME_DISPATCH_SLOTS", &c.DispatchSlots, 2},
		{"RUNTIME_TRAVEL_SLOTS", &c.TravelSlots, 1}, {"RUNTIME_PG_POOL_MAX", &c.PGPoolMax, 4},
		{"RUNTIME_OUTBOX_POLL_MILLIS", &c.OutboxPollMillis, 1000}, {"RUNTIME_OUTBOX_BATCH", &c.OutboxBatch, 100},
		{"RUNTIME_OUTBOX_LEASE_SECONDS", &c.OutboxLeaseSeconds, 30}, {"RUNTIME_SWEEP_MILLIS", &c.SweepMillis, 1000},
		{"RUNTIME_MAX_MODEL_ATTEMPTS", &c.MaxModelAttempts, 0}, {"RUNTIME_MAX_TOOL_ATTEMPTS", &c.MaxToolAttempts, 0},
	}
	for _, field := range ints {
		value := field.fallback
		if raw := get(field.key); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 32)
			if err != nil || parsed <= 0 {
				return fail(field.key)
			}
			value = int(parsed)
		}
		if value <= 0 {
			return fail(field.key)
		}
		*field.dst = value
	}
	for _, field := range []struct {
		key string
		dst *int64
	}{
		{"RUNTIME_MAX_TOKEN_BUDGET", &c.MaxTokenBudget}, {"RUNTIME_MODEL_INPUT_TOKENS", &c.ModelInputTokens}, {"RUNTIME_MODEL_OUTPUT_TOKENS", &c.ModelOutputTokens},
	} {
		value, err := strconv.ParseInt(get(field.key), 10, 64)
		if err != nil || value <= 0 {
			return fail(field.key)
		}
		*field.dst = value
	}
	// Subtract before comparing: adding two individually valid int64 limits can overflow.
	if c.ModelInputTokens > c.MaxTokenBudget || c.ModelOutputTokens > c.MaxTokenBudget-c.ModelInputTokens {
		return fail("RUNTIME_MODEL_INPUT_TOKENS/RUNTIME_MODEL_OUTPUT_TOKENS")
	}
	if c.ReturnReserveSeconds >= c.RunSeconds {
		return fail("RUNTIME_RETURN_RESERVE_SECONDS")
	}
	if c.ToolSeconds > c.RunSeconds-c.ReturnReserveSeconds {
		return fail("RUNTIME_TOOL_SECONDS")
	}
	if c.LeaseSeconds <= c.HeartbeatSeconds {
		return fail("RUNTIME_LEASE_SECONDS")
	}
	if c.PGPoolMax > 4 {
		return fail("RUNTIME_PG_POOL_MAX")
	}
	if err := json.Unmarshal([]byte(get("RUNTIME_RESOURCE_LIMITS")), &c.ResourceLimits); err != nil || len(c.ResourceLimits) == 0 {
		return fail("RUNTIME_RESOURCE_LIMITS")
	}
	for key, value := range c.ResourceLimits {
		if strings.TrimSpace(key) == "" || value <= 0 {
			return fail("RUNTIME_RESOURCE_LIMITS")
		}
	}
	return c, nil
}

func runtimeURL(raw string, schemes ...string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Fragment != "" {
		return false
	}
	for _, scheme := range schemes {
		if u.Scheme == scheme {
			return true
		}
	}
	return false
}

func runtimeMCPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
}
