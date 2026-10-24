package trip

import (
	"encoding/json"
	"slices"
	"testing"

	"agent-platform/backend/internal/model"
)

func TestTravelSourcePolicy(t *testing.T) {
	t.Run("resolve_only_mechanical_scope_and_date_gaps", func(t *testing.T) {
		policy := model.SourcePolicyContext{Status: "partial", Applicability: sourceConditions(`{"city":"杭州"}`), MissingFields: []string{"policy_applicability:city", "approval_evidence"}, Constraints: []model.SourcePolicyRule{}, Conflicts: []json.RawMessage{}}
		p := basePlan()
		resolved, err := ResolveSourcePolicyForPlan(policy, p, nil)
		if err != nil || slices.Contains(resolved.MissingFields, "policy_applicability:city") || !slices.Contains(resolved.MissingFields, "approval_evidence") || resolved.Status != "partial" {
			t.Fatal(resolved, err)
		}
		policy.MissingFields = []string{"policy_applicability:city"}
		resolved, err = ResolveSourcePolicyForPlan(policy, p, nil)
		if err != nil || resolved.Status != "ok" {
			t.Fatal(resolved, err)
		}
	})
	rule := model.SourcePolicyRule{ID: "seat", Rule: "高铁二等座", Field: "seat_class", Operator: "==", Value: json.RawMessage(`"二等座"`), Hard: true, EvidenceIDs: []string{"e"}, Applicability: map[string]json.RawMessage{}}
	policy := model.SourcePolicyContext{Status: "ok", Summary: "二等座", Applicability: map[string]json.RawMessage{}, Constraints: []model.SourcePolicyRule{rule}, References: []model.SourcePolicyReference{}, MissingFields: []string{}, Conflicts: []json.RawMessage{}}
	actual := []model.SourcePolicyReference{{ID: "e", Source: "real retrieved document"}}
	raw, _ := json.Marshal(policy)
	validated, err := ValidateSourcePolicy(raw, nil, actual)
	if err != nil || len(validated.References) != 1 || validated.References[0].Source != "real retrieved document" {
		t.Fatal(validated, err)
	}
	policy.Constraints[0].EvidenceIDs = []string{"invented"}
	raw, _ = json.Marshal(policy)
	if _, err = ValidateSourcePolicy(raw, nil, actual); err == nil {
		t.Fatal("invented citation accepted")
	}
	p := basePlan()
	p.SourceVersion = 1
	p.Tasks = p.Tasks[:1]
	p.RoundTrip = false
	p.PolicyContext = &validated
	p.Tasks[0].Train.Candidate.SeatClass = "一等座"
	v, _, err := validatePlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(v.Issues, func(i model.TripIssue) bool { return i.Code == "policy_constraint_failed" && i.TaskID == "t1" }) {
		t.Fatal("extracted hard seat policy did not constrain saved facts", v)
	}
	validated.Constraints[0].Applicability = sourceConditions(`{"city":"北京"}`)
	v, _, err = validatePlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(v.Issues, func(i model.TripIssue) bool { return i.Code == "policy_constraint_failed" }) {
		t.Fatal("city policy leaked into other segment")
	}
}
