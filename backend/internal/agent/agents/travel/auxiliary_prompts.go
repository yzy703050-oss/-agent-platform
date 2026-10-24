package travel

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
)

var (
	//go:embed prompts/policy_prefix.txt
	policyPrefix string
	//go:embed prompts/preference_prefix.txt
	preferencePrefix string
	//go:embed prompts/compaction_prefix.txt
	compactionPrefix string
)

// Preserve Python json.dumps' default separators for the original dynamic
// prompt, including the order of RawMessage fields in the fixed source golden.
func pythonJSON(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, buf.Bytes()); err != nil {
		return "", err
	}
	var out strings.Builder
	quoted, escaped := false, false
	for _, r := range compact.String() {
		out.WriteRune(r)
		if escaped {
			escaped = false
			continue
		}
		if quoted && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			quoted = !quoted
		}
		if !quoted && (r == ':' || r == ',') {
			out.WriteRune(' ')
		}
	}
	return out.String(), nil
}
func buildPolicyPrompt(context, evidence json.RawMessage) (string, error) {
	var fields struct {
		References []struct {
			ID string `json:"id"`
		} `json:"references"`
	}
	if err := json.Unmarshal(evidence, &fields); err != nil {
		return "", err
	}
	if len(fields.References) == 0 || fields.References[0].ID == "" {
		return "", errors.New("policy prompt needs actual evidence identity")
	}
	id, _ := json.Marshal(fields.References[0].ID)
	prefix := strings.ReplaceAll(policyPrefix, `"policy-1"`, string(id))
	data, err := pythonJSON(struct {
		Context  json.RawMessage `json:"user_context"`
		Evidence json.RawMessage `json:"evidence"`
	}{context, evidence})
	return prefix + data, err
}
func buildPreferencePrompt(history json.RawMessage, previous string, memories json.RawMessage) (string, error) {
	data, err := pythonJSON(struct {
		History  json.RawMessage `json:"history"`
		Summary  string          `json:"existing_summary"`
		Memories json.RawMessage `json:"memories"`
	}{history, previous, memories})
	return preferencePrefix + data, err
}
func buildCompactionPrompt(previous string, messages json.RawMessage) (string, error) {
	data, err := pythonJSON(struct {
		Previous string          `json:"previous_summary"`
		Messages json.RawMessage `json:"messages"`
	}{previous, messages})
	return compactionPrefix + data, err
}
