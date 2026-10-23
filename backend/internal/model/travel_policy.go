package model

import "encoding/json"

type PolicyConditions struct {
	Region        string `json:"region,omitempty"`
	EmployeeLevel string `json:"employee_level,omitempty"`
	EffectiveFrom string `json:"effective_from,omitempty"`
	EffectiveTo   string `json:"effective_to,omitempty"`
}
type PolicyFragment struct {
	ID         string           `json:"id"`
	Content    string           `json:"content"`
	Source     string           `json:"source"`
	Location   json.RawMessage  `json:"location,omitempty"`
	Score      *float64         `json:"score,omitempty"`
	Conditions PolicyConditions `json:"conditions"`
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
