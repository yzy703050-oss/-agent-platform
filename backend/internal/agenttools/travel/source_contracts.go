package travel

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

//go:embed schemas/source_contracts.json
var sourceContracts []byte

//go:embed schemas/trip_argument_help.txt
var TripArgumentHelp string

// SourceProfile returns fresh definitions so per-run filtering cannot mutate
// another execution's schemas. Snapshots are exported from the frozen source
// ToolDefinition.schema, including its original descriptions.
func SourceProfile(profile string) ([]*schema.ToolInfo, error) {
	var profiles map[string][]struct {
		Function struct {
			Name        string             `json:"name"`
			Description string             `json:"description"`
			Parameters  *jsonschema.Schema `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(sourceContracts, &profiles); err != nil {
		return nil, err
	}
	defs, ok := profiles[profile]
	if !ok {
		return nil, fmt.Errorf("unknown travel source profile: %s", profile)
	}
	infos := make([]*schema.ToolInfo, 0, len(defs))
	for _, def := range defs {
		infos = append(infos, &schema.ToolInfo{Name: def.Function.Name, Desc: def.Function.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(def.Function.Parameters)})
	}
	return infos, nil
}
