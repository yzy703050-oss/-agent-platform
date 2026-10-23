package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

type Config struct {
	PublicOrigin                                                                         string
	CookieSecure                                                                         bool
	DatabaseDSN, HTTPAddr, ModelBaseURL, ModelID, ModelAPIKey                            string
	MaxIterations, RunSeconds, ToolSeconds, HeartbeatSeconds, LeaseSeconds, PauseSeconds int
}

func Load(get func(string) string) (Config, error) {
	c := Config{DatabaseDSN: get("DB_DSN"), HTTPAddr: get("HTTP_ADDR"), ModelBaseURL: get("LLM_BASE_URL"), ModelID: get("LLM_MODEL"), ModelAPIKey: get("LLM_API_KEY")}
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
