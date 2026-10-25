package config

import (
	"strings"
	"testing"
)

func TestWebSearchConfiguration(t *testing.T) {
	for _, tc := range []struct {
		raw              string
		enabled, invalid bool
	}{{"", true, false}, {"true", true, false}, {"false", false, false}, {"typo", false, true}} {
		cfg, err := Load(func(key string) string {
			switch key {
			case "DB_DSN":
				return "postgres://local/database"
			case "WEB_SEARCH_ENABLED":
				return tc.raw
			}
			return ""
		})
		if (err != nil) != tc.invalid {
			t.Fatalf("value=%q err=%v", tc.raw, err)
		}
		if err == nil && cfg.WebSearchEnabled != tc.enabled {
			t.Fatalf("value=%q enabled=%v", tc.raw, cfg.WebSearchEnabled)
		}
	}
}

func TestDatabaseRequired(t *testing.T) {
	_, e := Load(func(string) string { return "" })
	if e == nil || !strings.Contains(e.Error(), "DB_DSN") {
		t.Fatal("must require real database")
	}
}

func TestInvalidTravelModeRejected(t *testing.T) {
	_, err := Load(func(k string) string {
		if k == "DB_DSN" {
			return "postgres://local/database"
		}
		if k == "TRAVEL_QUERY_MODE" {
			return "typo"
		}
		return ""
	})
	if err == nil {
		t.Fatal("invalid mode silently accepted")
	}
}

func TestProductionCookieCannotBeInsecure(t *testing.T) {
	_, err := Load(func(k string) string {
		switch k {
		case "DB_DSN":
			return "postgres://local/database"
		case "PUBLIC_ORIGIN":
			return "https://example.test"
		case "COOKIE_SECURE":
			return "false"
		}
		return ""
	})
	if err == nil {
		t.Fatal("production insecure cookie accepted")
	}
}
func TestDefaultLimits(t *testing.T) {
	c, e := Load(func(k string) string {
		if k == "DB_DSN" {
			return "postgres://local/database"
		}
		return ""
	})
	if e != nil {
		t.Fatal(e)
	}
	if c.MaxIterations != 8 || c.RunSeconds != 120 || c.ToolSeconds != 10 || c.LeaseSeconds != 20 || c.HeartbeatSeconds != 5 || c.PauseSeconds != 15 {
		t.Fatalf("wrong defaults: %+v", c)
	}
}
func TestInvalidLimitRejected(t *testing.T) {
	_, e := Load(func(k string) string {
		switch k {
		case "DB_DSN":
			return "postgres://local/database"
		case "MAX_ITERATIONS":
			return "-1"
		}
		return ""
	})
	if e == nil {
		t.Fatal("invalid budget accepted")
	}
}
func TestOriginTrailingSlashNormalized(t *testing.T) {
	c, e := Load(func(k string) string {
		if k == "DB_DSN" {
			return "postgres://local/database"
		}
		if k == "PUBLIC_ORIGIN" {
			return "http://127.0.0.1:5173/"
		}
		return ""
	})
	if e != nil || c.PublicOrigin != "http://127.0.0.1:5173" {
		t.Fatalf("origin: %q %v", c.PublicOrigin, e)
	}
}
