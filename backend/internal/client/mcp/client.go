package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"agent-platform/backend/internal/model"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// StdioConfig is operator configuration, never model or HTTP request input.
type StdioConfig struct {
	Command, Directory, Collection string
	SettingsPath                   string
	Args                           []string
}
type Client struct {
	cfg     StdioConfig
	mu      sync.Mutex
	session *sdk.ClientSession
	closed  bool
}

func New(cfg StdioConfig) (*Client, error) {
	if !filepath.IsAbs(cfg.Command) || !filepath.IsAbs(cfg.Directory) || cfg.Collection == "" {
		return nil, model.ErrInvalidInput
	}
	if cfg.SettingsPath != "" && !filepath.IsAbs(cfg.SettingsPath) {
		return nil, model.ErrInvalidInput
	}
	if _, err := NewPolicyClient(&unconnected{}, cfg.Collection); err != nil {
		return nil, err
	}
	return &Client{cfg: cfg}, nil
}

type unconnected struct{}

func (*unconnected) CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error) {
	return nil, errors.New("not connected")
}
func (c *Client) connect(ctx context.Context) (*sdk.ClientSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("policy client closed")
	}
	if c.session != nil {
		return c.session, nil
	}
	cmd := exec.Command(c.cfg.Command, c.cfg.Args...)
	cmd.Dir = c.cfg.Directory
	cmd.Stderr = io.Discard
	// Do not pass platform DB/LLM credentials to a separate process.
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "HOME", "APPDATA", "LOCALAPPDATA"} {
		if v := os.Getenv(name); v != "" {
			cmd.Env = append(cmd.Env, name+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if c.cfg.SettingsPath != "" {
		cmd.Env = append(cmd.Env, "MODULAR_RAG_SETTINGS_PATH="+c.cfg.SettingsPath)
	}
	hideProcess(cmd)
	client := sdk.NewClient(&sdk.Implementation{Name: "agent-platform", Version: "travel-v1"}, &sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: cmd, TerminateDuration: time.Second}, nil)
	if err != nil {
		return nil, errors.New("policy MCP connection unavailable")
	}
	found := false
	for info, err := range session.Tools(ctx, nil) {
		if err != nil {
			session.Close()
			return nil, errors.New("policy MCP tools unavailable")
		}
		if info.Name != "query_policy_evidence" {
			continue
		}
		data, _ := json.Marshal(info.InputSchema)
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if json.Unmarshal(data, &schema) != nil || schema.Type != "object" || schema.Properties["query"] == nil || schema.Properties["collection"] == nil {
			session.Close()
			return nil, ErrProtocol
		}
		found = true
	}
	if !found {
		session.Close()
		return nil, errors.New("policy MCP tool unavailable")
	}
	c.session = session
	return session, nil
}
func (c *Client) Search(ctx context.Context, q string) (model.PolicyEvidence, error) {
	session, err := c.connect(ctx)
	if err != nil {
		return model.PolicyEvidence{}, err
	}
	p, err := NewPolicyClient(session, c.cfg.Collection)
	if err != nil {
		return model.PolicyEvidence{}, err
	}
	return p.Search(ctx, q)
}
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}
