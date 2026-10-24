package model

import "encoding/json"

// TravelRuleError is a correctable business observation. Infrastructure errors
// do not use this type and must never be serialized as model instructions.
type TravelRuleError struct {
	Code    string                     `json:"code"`
	Message string                     `json:"message"`
	Details map[string]json.RawMessage `json:"details,omitempty"`
}

func (e *TravelRuleError) Error() string    { return e.Code + ": " + e.Message }
func (e *TravelRuleError) Unwrap() error    { return ErrInvalidInput }
func (e *TravelRuleError) RuleCode() string { return e.Code }
