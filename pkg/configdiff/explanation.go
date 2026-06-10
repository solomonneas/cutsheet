package configdiff

type ExplanationProvider interface {
	Render(Analysis) (map[string]string, error)
}

type deterministicProvider struct{}

func (deterministicProvider) Render(analysis Analysis) (map[string]string, error) {
	return map[string]string{
		"change-summary.md":     renderChangeSummary(analysis),
		"risk-analysis.md":      renderRiskAnalysis(analysis),
		"touched-objects.md":    renderTouchedObjects(analysis),
		"rollback-plan.md":      renderRollbackPlan(analysis),
		"validation-plan.md":    renderValidationPlan(analysis),
		"operator-checklist.md": renderOperatorChecklist(analysis),
		"stakeholder-brief.md":  renderStakeholderBrief(analysis),
	}, nil
}
