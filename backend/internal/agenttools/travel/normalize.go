package travel

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"time"
)

var nativeFilterKeys = []string{"hotel_max_total_cny", "hotel_max_nightly_cny", "train_max_total_cny", "seat_class", "train_kind", "departure_time_after", "departure_time_before", "available_only", "total_budget_cny"}

// NormalizeNativeArguments mirrors source normalization. It canonicalizes
// representation only; it never guesses dates, IDs, requirements or strengths.
func NormalizeNativeArguments(name string, data json.RawMessage, timezone, role string) (json.RawMessage, error) {
	var root map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&root); err != nil || root == nil {
		return nil, ruleError("INVALID_ARGUMENTS", "tool arguments must be an object", nil)
	}
	// Validate trailing input before canonical serialization can hide it.
	var extra any
	if err := strictDecode(data, &extra); err != nil {
		return nil, ruleError("INVALID_ARGUMENTS", "invalid tool JSON", nil)
	}
	if name == "write_trip" {
		if plan, ok := root["tripplan"].(map[string]any); ok {
			if _, exists := plan["operation"]; !exists {
				op := "propose"
				if role == "verifier" {
					op = "select"
				}
				plan["operation"] = op
			}
		}
	}
	var walk func(any, string) (any, error)
	walk = func(value any, path string) (any, error) {
		switch node := value.(type) {
		case []any:
			for i, v := range node {
				next, err := walk(v, path)
				if err != nil {
					return nil, err
				}
				node[i] = next
			}
			return node, nil
		case map[string]any:
			cleaned := map[string]any{}
			for key, v := range node {
				if key == "arrival_before" {
					if text, ok := v.(string); ok {
						if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
							if timezone != "Asia/Shanghai" {
								return nil, ruleError("INVALID_ARGUMENTS", "arrival_before requires explicit timezone", nil)
							}
							parsed := time.Time{}
							for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
								t, e := time.ParseInLocation(layout, text, time.FixedZone("Asia/Shanghai", 8*3600))
								if e == nil {
									parsed = t
									break
								}
							}
							if parsed.IsZero() {
								return nil, ruleError("INVALID_ARGUMENTS", "invalid arrival_before date/time", nil)
							}
							v = parsed.Format(time.RFC3339Nano)
						}
					}
				} else if key == "field_sources" || key == "condition_strengths" {
					if values, ok := v.(map[string]any); ok {
						normalized, err := normalizeMetadata(values, node, path+"."+key, key)
						if err != nil {
							return nil, err
						}
						v = normalized
					}
				} else {
					next, err := walk(v, path+"."+key)
					if err != nil {
						return nil, err
					}
					v = next
				}
				if name == "write_trip" && (key == "conditions" || key == "confirmed_conditions") {
					if fields, ok := v.(map[string]any); ok {
						constraints, _ := fields["constraints"].(map[string]any)
						for leaf, proposed := range fields {
							if slices.Contains(nativeFilterKeys, leaf) {
								if constraints == nil {
									constraints = map[string]any{}
									fields["constraints"] = constraints
								}
								if old, exists := constraints[leaf]; exists && !equivalentNative(old, proposed) {
									return nil, ruleError("INVALID_ARGUMENTS", "conflicting constraint: "+leaf, nil)
								}
								constraints[leaf] = proposed
								delete(fields, leaf)
							}
						}
					}
				}
				cleaned[key] = v
			}
			return cleaned, nil
		}
		return value, nil
	}
	if name == "write_trip" || name == "query" {
		field := "tripplan"
		if name == "query" {
			field = "query_plan"
		}
		if value, ok := root[field]; ok {
			next, err := walk(value, field)
			if err != nil {
				return nil, err
			}
			root[field] = next
		}
	}
	return json.Marshal(root)
}

func normalizeMetadata(values, parent map[string]any, path, name string) (map[string]any, error) {
	conditions, _ := parent["conditions"].(map[string]any)
	if conditions == nil {
		conditions, _ = parent["confirmed_conditions"].(map[string]any)
	}
	if conditions == nil {
		conditions = parent
	}
	declared, _ := conditions["constraints"].(map[string]any)
	safe := []string{"user", "context", "preference", "derived", "default", "proposal"}
	if name == "condition_strengths" {
		safe = []string{"hard", "soft"}
	}
	invalid := func(field string) error {
		detail := map[string]any{"field": path + "." + field, "type": "invalid_constraint_metadata", "message": "约束来源和强度须按叶字段填写，不能给constraints整体标注或使用未知键。", "expected": "以train_kind等具体约束叶字段为键；仅重发受影响的最小补丁。"}
		return ruleError("INVALID_ARGUMENTS", "unknown constraint metadata field: "+field, map[string]json.RawMessage{"fields": mustJSON([]any{detail})})
	}
	out := map[string]any{}
	for key, value := range values {
		entries := map[string]any{key: value}
		if key == "constraints" {
			if nested, ok := value.(map[string]any); ok {
				entries = nested
			} else if text, ok := value.(string); ok && len(declared) == 1 && slices.Contains(safe, text) {
				for leaf := range declared {
					if !slices.Contains(nativeFilterKeys, leaf) {
						return nil, invalid(key)
					}
					entries = map[string]any{leaf: text}
				}
			} else {
				return nil, invalid(key)
			}
		}
		for field, proposed := range entries {
			leaf := strings.TrimPrefix(field, "constraints.")
			target := field
			if slices.Contains(nativeFilterKeys, leaf) {
				target = leaf
			}
			if key == "constraints" && !slices.Contains(nativeFilterKeys, target) {
				return nil, invalid(key + "." + field)
			}
			if previous, ok := out[target]; ok && !reflect.DeepEqual(previous, proposed) {
				return nil, ruleError("INVALID_ARGUMENTS", "conflicting metadata: "+target, map[string]json.RawMessage{"fields": mustJSON([]any{map[string]string{"field": path + "." + target, "type": "conflicting_metadata", "message": "同一叶字段存在冲突的元数据，保留一个明确值。"}})})
			}
			out[target] = proposed
		}
	}
	return out, nil
}
func equivalentNative(a, b any) bool {
	x, xok := a.(json.Number)
	y, yok := b.(json.Number)
	if xok && yok {
		rx, ok := new(big.Rat).SetString(string(x))
		ry, ok2 := new(big.Rat).SetString(string(y))
		return ok && ok2 && rx.Cmp(ry) == 0
	}
	return reflect.DeepEqual(a, b)
}
func mustJSON(value any) json.RawMessage { b, _ := json.Marshal(value); return b }
