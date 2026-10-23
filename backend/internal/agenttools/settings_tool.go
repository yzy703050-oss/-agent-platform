// Package agenttools bridges authorized business services to Eino tools.
package agenttools

import (
	"agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"time"
)

type SettingsReader interface {
	Get(context.Context, model.Scope) (model.UserSettings, error)
}
type SettingsTool struct {
	reader  SettingsReader
	scope   model.Scope
	timeout time.Duration
}

func NewSettingsTool(r SettingsReader, s model.Scope, timeout time.Duration) *SettingsTool {
	return &SettingsTool{r, s, timeout}
}
func (t *SettingsTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "get_my_settings", Desc: "读取当前登录用户的界面主题与回复偏好。无需参数。", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}, nil
}
func (t *SettingsTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(args), &fields) != nil || fields == nil || len(fields) != 0 {
		return "", errors.New("tool accepts an empty object only")
	}
	bounded, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	s, e := t.reader.Get(bounded, t.scope)
	if e != nil {
		return "", errors.New("settings unavailable")
	}
	b, e := json.Marshal(s)
	return string(b), e
}
