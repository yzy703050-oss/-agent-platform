package travel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const regenerationInstruction = "上一次模型响应无正文或原生调用结构无效，未执行任何半截动作。只在当前工具仍允许时重新生成完整合法调用，否则基于实际结果给出自然答复。保留当前权限、有效进度和未完成原因，不要求用户调整硬条件。"

var ErrSourceModelProtocol = errors.New("source model response did not complete after two regenerations")

func sourceProtocolFailure(response *schema.Message) string {
	if response == nil {
		return "invalid_model_response"
	}
	if response.ResponseMeta != nil {
		finish := response.ResponseMeta.FinishReason
		if finish != "stop" && finish != "tool_calls" {
			return "invalid_model_response"
		}
		if len(response.ToolCalls) > 0 && finish != "tool_calls" {
			return "invalid_model_response"
		}
	}
	if len(response.ToolCalls) == 0 && strings.TrimSpace(response.Content) == "" {
		return "empty_model_response"
	}
	ids := map[string]bool{}
	for _, call := range response.ToolCalls {
		if call.ID == "" || call.Function.Name == "" || ids[call.ID] {
			return "invalid_model_response"
		}
		ids[call.ID] = true
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(call.Function.Arguments), &object) != nil || object == nil {
			return "invalid_model_response"
		}
	}
	return ""
}
func withRegenerationInstruction(messages []*schema.Message) []*schema.Message {
	for _, msg := range messages {
		if msg.Role == schema.System && msg.Content == regenerationInstruction {
			return messages
		}
	}
	at := min(1, len(messages))
	out := append([]*schema.Message(nil), messages[:at]...)
	out = append(out, schema.SystemMessage(regenerationInstruction))
	return append(out, messages[at:]...)
}
func generateSourceTurn(ctx context.Context, inner em.BaseChatModel, input []*schema.Message, flow *sourceFlow, opts ...em.Option) (*schema.Message, []*schema.Message, error) {
	for {
		response, err := inner.Generate(ctx, input, opts...)
		if err != nil {
			return response, input, err
		}
		failure := sourceProtocolFailure(response)
		if failure == "" {
			return response, input, nil
		}
		s := FlowState{Version: 1, Phase: "deciding", Role: "main"}
		if value, ok := adk.GetSessionValue(ctx, flowKey); ok {
			text, ok := value.(string)
			if !ok || json.Unmarshal([]byte(text), &s) != nil {
				return nil, input, ErrSourceModelProtocol
			}
		}
		s.GenerationFailures++
		if len(s.Plans) > 0 && s.Role == "verifier" {
			s.ObserveReview()
		}
		if s.GenerationFailures > 2 {
			s.StopReason = failure
			s.Phase = "stopping"
		}
		input = withRegenerationInstruction(input)
		s.PreparedMessages = input
		if flow != nil {
			flow.mu.Lock()
			err = flow.save(ctx, s)
			flow.mu.Unlock()
		} else {
			var raw []byte
			raw, err = json.Marshal(s)
			if err == nil {
				adk.AddSessionValue(ctx, flowKey, string(raw))
			}
		}
		if err != nil {
			return nil, input, err
		}
		if s.StopReason == "no_progress" {
			final, err := stoppedSourceReply(s)
			if err != nil {
				return nil, input, err
			}
			batch, err := loadNativeBatch(ctx)
			if err != nil {
				return nil, input, err
			}
			batch.Final = final
			if err = saveNativeBatch(ctx, batch); err != nil {
				return nil, input, err
			}
			return schema.AssistantMessage(final, nil), input, nil
		}
		if s.GenerationFailures > 2 {
			return nil, input, ErrSourceModelProtocol
		}
	}
}
