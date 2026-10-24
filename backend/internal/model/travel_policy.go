package model

import "encoding/json"

type SourcePolicyRule struct {
	ID            string                     `json:"id"`
	Rule          string                     `json:"rule"`
	Applicability map[string]json.RawMessage `json:"applicability"`
	Hard          bool                       `json:"hard"`
	EvidenceIDs   []string                   `json:"evidence_ids"`
	Field         string                     `json:"field,omitempty"`
	Operator      string                     `json:"operator,omitempty"`
	Value         json.RawMessage            `json:"value,omitempty"`
	Unit          *string                    `json:"unit"`
}
type SourcePolicyContext struct {
	Status        string                     `json:"status"`
	Summary       string                     `json:"summary"`
	Constraints   []SourcePolicyRule         `json:"constraints"`
	Applicability map[string]json.RawMessage `json:"applicability"`
	References    []SourcePolicyReference    `json:"references"`
	EffectiveDate *string                    `json:"effective_date"`
	MissingFields []string                   `json:"missing_fields"`
	Conflicts     []json.RawMessage          `json:"conflicts"`
}

type SourcePolicyReference struct {
	ID            string          `json:"id"`
	DocumentID    json.RawMessage `json:"document_id,omitempty"`
	ChunkID       json.RawMessage `json:"chunk_id,omitempty"`
	Source        string          `json:"source,omitempty"`
	Location      json.RawMessage `json:"location,omitempty"`
	Version       json.RawMessage `json:"version,omitempty"`
	EffectiveDate json.RawMessage `json:"effective_date,omitempty"`
	Title         json.RawMessage `json:"title,omitempty"`
}

type PolicyConditions struct {
	Region        string `json:"region,omitempty"`
	EmployeeLevel string `json:"employee_level,omitempty"`
	EffectiveFrom string `json:"effective_from,omitempty"`
	EffectiveTo   string `json:"effective_to,omitempty"`
}
type PolicyFragment struct {
	Metadata   map[string]json.RawMessage `json:"metadata,omitempty"`
	ID         string                     `json:"id"`
	Content    string                     `json:"content"`
	Source     string                     `json:"source"`
	Location   json.RawMessage            `json:"location,omitempty"`
	Score      *float64                   `json:"score,omitempty"`
	Conditions PolicyConditions           `json:"conditions"`
}
type PolicyEvidence struct {
	IsDemo     bool             `json:"is_demo,omitempty"`
	Status     string           `json:"status"`
	Collection string           `json:"collection,omitempty"`
	Fragments  []PolicyFragment `json:"fragments,omitempty"`
}
type PolicyRequest struct {
	Query         string `json:"query"`
	Region        string `json:"region,omitempty"`
	EmployeeLevel string `json:"employee_level,omitempty"`
	Date          string `json:"date,omitempty"`
}
type PolicyAssessment struct {
	Evidence      PolicyEvidence `json:"evidence"`
	Applicability string         `json:"applicability"`
	Compliance    string         `json:"compliance"`
	Reason        string         `json:"reason"`
}
