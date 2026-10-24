package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	TravelPlanningRounds, TravelTurnSeconds, TravelContextTokens                         int
	TravelQueryMode                                                                      string
	TrainAPIKey, AMAPAPIKey                                                              string
	RAGMCPCommand, RAGMCPDir, RAGCollection                                              string
	RAGSettingsPath                                                                      string
	RAGMCPArgs                                                                           []string
	PublicOrigin                                                                         string
	CookieSecure                                                                         bool
	DatabaseDSN, HTTPAddr, ModelBaseURL, ModelID, ModelAPIKey                            string
	MaxIterations, RunSeconds, ToolSeconds, HeartbeatSeconds, LeaseSeconds, PauseSeconds int
}

func Load(get func(string) string) (Config, error) {
	c := Config{DatabaseDSN: get("DB_DSN"), HTTPAddr: get("HTTP_ADDR"), ModelBaseURL: get("LLM_BASE_URL"), ModelID: get("LLM_MODEL"), ModelAPIKey: get("LLM_API_KEY")}
	c.TrainAPIKey, c.AMAPAPIKey = get("TRAIN_API_KEY"), get("AMAP_API_KEY")
	c.TravelQueryMode = get("TRAVEL_QUERY_MODE")
	if c.TravelQueryMode == "" {
		c.TravelQueryMode = "live"
	}
	if c.TravelQueryMode != "live" && c.TravelQueryMode != "demo" {
		return c, errors.New("TRAVEL_QUERY_MODE must be live or demo")
	}
	c.RAGMCPCommand, c.RAGMCPDir, c.RAGCollection = get("RAG_MCP_COMMAND"), get("RAG_MCP_DIR"), get("RAG_COLLECTION")
	c.RAGSettingsPath = get("RAG_SETTINGS_PATH")
	c.RAGMCPArgs = []string{"-m", "src.mcp_server.server"}
	if args := get("RAG_MCP_ARGS"); args != "" {
		if json.Unmarshal([]byte(args), &c.RAGMCPArgs) != nil {
			return c, errors.New("RAG_MCP_ARGS must be a JSON string array")
		}
	}
	if c.DatabaseDSN == "" {
		return c, errors.New("DB_DSN is required; no in-memory database fallback")
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = "127.0.0.1:8080"
	}
	c.PublicOrigin = get("PUBLIC_ORIGIN")
	if c.PublicOrigin == "" {
		c.PublicOrigin = "http://127.0.0.1:5173"
	}
	origin, err := url.Parse(c.PublicOrigin)
	if err != nil || origin.Host == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return c, errors.New("invalid PUBLIC_ORIGIN")
	}
	c.PublicOrigin = strings.TrimSuffix(c.PublicOrigin, "/")
	c.CookieSecure = origin.Scheme == "https"
	if raw := get("COOKIE_SECURE"); raw != "" {
		c.CookieSecure, err = strconv.ParseBool(raw)
		if err != nil {
			return c, errors.New("invalid COOKIE_SECURE")
		}
	}
	if !c.CookieSecure && origin.Hostname() != "localhost" && origin.Hostname() != "127.0.0.1" && origin.Hostname() != "::1" {
		return c, errors.New("insecure cookies are only allowed for local development")
	}
	limits := []struct {
		name     string
		fallback int
		target   *int
	}{
		{"MAX_ITERATIONS", 8, &c.MaxIterations}, {"RUN_SECONDS", 120, &c.RunSeconds}, {"TOOL_SECONDS", 10, &c.ToolSeconds},
		{"TRAVEL_PLANNING_ROUNDS", 8, &c.TravelPlanningRounds}, {"TRAVEL_TURN_SECONDS", 600, &c.TravelTurnSeconds}, {"TRAVEL_CONTEXT_TOKENS", 20000, &c.TravelContextTokens},
		{"HEARTBEAT_SECONDS", 5, &c.HeartbeatSeconds}, {"LEASE_SECONDS", 20, &c.LeaseSeconds}, {"PAUSE_SECONDS", 15, &c.PauseSeconds},
	}
	for _, limit := range limits {
		*limit.target = limit.fallback
		if value := get(limit.name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return c, fmt.Errorf("%s must be a positive integer", limit.name)
			}
			*limit.target = n
		}
	}
	if c.LeaseSeconds <= c.HeartbeatSeconds {
		return c, errors.New("LEASE_SECONDS must exceed HEARTBEAT_SECONDS")
	}
	return c, nil
}
