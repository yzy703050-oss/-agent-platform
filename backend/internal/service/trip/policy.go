package trip

import (
	"context"
	"strings"
	"time"

	"agent-platform/backend/internal/model"
)

type PolicyRetriever interface {
	Search(context.Context, string) (model.PolicyEvidence, error)
}
type PolicyService struct{ retriever PolicyRetriever }

func NewPolicyService(r PolicyRetriever) *PolicyService { return &PolicyService{retriever: r} }
func (s *PolicyService) Check(ctx context.Context, q model.PolicyRequest) (model.PolicyAssessment, error) {
	out := model.PolicyAssessment{Applicability: "unverified", Compliance: "unverified", Reason: "政策检索尚未配置。"}
	if strings.TrimSpace(q.Query) == "" || len(q.Query) > 4000 {
		return out, model.ErrInvalidInput
	}
	if q.Date != "" {
		if _, err := time.Parse("2006-01-02", q.Date); err != nil {
			return out, model.ErrInvalidInput
		}
	}
	if s == nil || s.retriever == nil {
		return out, nil
	}
	e, err := s.retriever.Search(ctx, q.Query)
	if err != nil {
		out.Reason = "政策检索失败或超时，尚未核验。"
		return out, nil
	}
	out.Evidence = e
	if e.Status != "ok" || len(e.Fragments) == 0 {
		out.Evidence.Fragments = nil
		out.Reason = "没有可核验的政策依据。"
		return out, nil
	}
	matched, unknown := false, false
	for _, f := range e.Fragments {
		c := f.Conditions
		if (c.Region != "" && q.Region != "" && c.Region != q.Region) || (c.EmployeeLevel != "" && q.EmployeeLevel != "" && c.EmployeeLevel != q.EmployeeLevel) {
			continue
		}
		if (c.Region != "" && q.Region == "") || (c.EmployeeLevel != "" && q.EmployeeLevel == "") {
			unknown = true
			continue
		}
		if c.EffectiveFrom != "" || c.EffectiveTo != "" {
			if q.Date == "" {
				unknown = true
				continue
			}
			valid := true
			for _, date := range []string{c.EffectiveFrom, c.EffectiveTo} {
				if date != "" {
					if _, err := time.Parse("2006-01-02", date); err != nil {
						valid = false
					}
				}
			}
			if !valid {
				unknown = true
				continue
			}
			if (c.EffectiveFrom != "" && q.Date < c.EffectiveFrom) || (c.EffectiveTo != "" && q.Date > c.EffectiveTo) {
				continue
			}
		}
		// Missing machine-readable conditions does not establish applicability.
		if c.Region == "" && c.EmployeeLevel == "" && c.EffectiveFrom == "" && c.EffectiveTo == "" {
			unknown = true
			continue
		}
		matched = true
	}
	if matched {
		out.Applicability = "applicable"
		out.Reason = "有适用范围匹配的政策片段；检索命中不等于行程合规，仍需逐项核验规则。"
	} else if !unknown {
		out.Applicability = "not_applicable"
		out.Reason = "已返回片段的适用范围与本次条件不符。"
	} else {
		out.Reason = "已检索到片段，适用信息不足，尚未核验。"
	}
	return out, nil
}
