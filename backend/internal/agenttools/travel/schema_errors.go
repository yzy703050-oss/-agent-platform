package travel

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"agent-platform/backend/internal/model"
	js "github.com/google/jsonschema-go/jsonschema"
)

var validationSchemaPath = regexp.MustCompile(`^validating ([^:\n]+):`)

func nativeSchemaError(err error, validators ...*js.Resolved) error {
	field := "arguments"
	allNames := []string{}
	lastPath := ""
	// The library's final error may echo input values. Extract only a schema
	// pointer from its wrappers, and keep the correction text value-free.
	for current := err; current != nil; current = errors.Unwrap(current) {
		match := validationSchemaPath.FindStringSubmatch(current.Error())
		if len(match) < 2 {
			continue
		}
		path := match[1]
		if strings.HasPrefix(path, "/") {
			lastPath = path
			pieces := strings.Split(path, "/")
			names := []string{}
			for i := 0; i+1 < len(pieces); i++ {
				if pieces[i] == "properties" {
					names = append(names, pieces[i+1])
				}
				if pieces[i] == "items" {
					names = append(names, "*")
				}
			}
			if len(names) > 0 {
				overlap := min(len(allNames), len(names))
				for overlap > 0 {
					same := true
					for i := 0; i < overlap; i++ {
						same = same && allNames[len(allNames)-overlap+i] == names[i]
					}
					if same {
						break
					}
					overlap--
				}
				allNames = append(allNames, names[overlap:]...)
				field = strings.Join(allNames, ".")
			}
		}
	}
	// Named $defs can hide the array-items wrapper in this validator's error.
	// These are fixed native schema collection fields, not input-supplied keys.
	if len(allNames) > 0 {
		path := []string{}
		for i, name := range allNames {
			path = append(path, name)
			if i+1 < len(allNames) && allNames[i+1] != "*" && slices.Contains([]string{"tasks", "route_tasks", "items", "content", "updates", "query_plan"}, name) {
				path = append(path, "*")
			}
		}
		field = strings.Join(path, ".")
	}
	message := "检查此字段的必需项、类型、operation枚举和多余字段；按当前工具schema修正。"
	if len(validators) > 0 && lastPath != "" {
		// All hint values come from the frozen schema, never from the rejected
		// instance or the validator's value-echoing error text.
		raw, _ := json.Marshal(validators[0].Schema())
		var doc any
		_ = json.Unmarshal(raw, &doc)
		for _, key := range strings.Split(strings.TrimPrefix(lastPath, "/"), "/") {
			object, ok := doc.(map[string]any)
			if !ok {
				doc = nil
				break
			}
			doc = object[strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")]
		}
		if leaf, ok := doc.(map[string]any); ok {
			if allowed, ok := leaf["enum"]; ok {
				values, _ := json.Marshal(allowed)
				message = "此字段必须使用当前阶段允许的枚举：" + string(values)
			} else if required, ok := leaf["required"]; ok {
				values, _ := json.Marshal(required)
				message = "检查该对象的必填字段：" + string(values)
			} else if leaf["additionalProperties"] == false && leaf["properties"] != nil {
				properties, _ := leaf["properties"].(map[string]any)
				keys := make([]string, 0, len(properties))
				for key := range properties {
					keys = append(keys, key)
				}
				slices.Sort(keys)
				value, _ := json.Marshal(keys)
				message = "此对象不允许额外字段；允许的字段为：" + string(value)
			} else if kind, ok := leaf["type"]; ok {
				value, _ := json.Marshal(kind)
				message = "此字段要求类型：" + string(value)
			}
		}
	}
	fields, _ := json.Marshal([]map[string]string{{"field": field, "type": "schema_validation", "message": message}})
	return &model.TravelRuleError{Code: "INVALID_ARGUMENTS", Message: "参数不符合当前工具结构。", Details: map[string]json.RawMessage{"fields": fields}}
}
