package travel

import (
	"encoding/json"
	"regexp"
	"strings"

	"agent-platform/backend/internal/model"
)

var validationSchemaPath = regexp.MustCompile(`validating ([^:\n]+):`)

func nativeSchemaError(err error) error {
	field := "arguments"
	// The library's final error may echo input values. Extract only a schema
	// pointer from its wrappers, and keep the correction text value-free.
	for _, match := range validationSchemaPath.FindAllStringSubmatch(err.Error(), -1) {
		path := match[1]
		if strings.HasPrefix(path, "/") && !strings.Contains(path, "$defs") {
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
				field = strings.Join(names, ".")
			}
		}
	}
	fields, _ := json.Marshal([]map[string]string{{"field": field, "type": "schema_validation", "message": "检查此字段的必需项、类型、operation枚举和多余字段；按当前工具schema修正。"}})
	return &model.TravelRuleError{Code: "INVALID_ARGUMENTS", Message: "参数不符合当前工具结构。", Details: map[string]json.RawMessage{"fields": fields}}
}
