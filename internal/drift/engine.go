package drift

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/driftctl/driftctl/internal/model"
)

// Engine compares expected and actual resource sets.
type Engine struct {
	IgnoreTags       map[string]bool
	IgnoreAttributes map[string]bool
}

func NewEngine(cfg model.CompareConfig) *Engine {
	e := &Engine{
		IgnoreTags:       make(map[string]bool),
		IgnoreAttributes: make(map[string]bool),
	}
	for _, t := range cfg.IgnoreTags {
		e.IgnoreTags[t] = true
	}
	for _, a := range cfg.IgnoreAttributes {
		e.IgnoreAttributes[a] = true
	}
	// Always ignore terraform-managed tags by default
	for _, t := range []string{"terraform", "driftctl_scan"} {
		e.IgnoreTags[t] = true
	}
	return e
}

// Compare produces a drift report from expected (state) and actual (cloud) resources.
func (e *Engine) Compare(expected, actual []model.Resource) []model.DriftFinding {
	actualIndex := make(map[string]model.Resource, len(actual))
	for _, r := range actual {
		actualIndex[r.ID] = r
	}

	var findings []model.DriftFinding

	for _, exp := range expected {
		act, ok := actualIndex[exp.ID]
		if !ok {
			findings = append(findings, model.DriftFinding{
				Kind:         model.DriftMissingInCloud,
				ResourceID:   exp.ID,
				ResourceType: exp.Type,
				ResourceName: exp.Name,
				Severity:     severityForMissing(exp),
			})
			continue
		}
		findings = append(findings, e.diffAttributes(exp, act)...)
		findings = append(findings, e.diffTags(exp, act)...)
	}

	return findings
}

func (e *Engine) diffAttributes(exp, act model.Resource) []model.DriftFinding {
	var findings []model.DriftFinding
	allKeys := make(map[string]bool)
	for k := range exp.Attributes {
		allKeys[k] = true
	}
	for k := range act.Attributes {
		allKeys[k] = true
	}

	for key := range allKeys {
		if e.IgnoreAttributes[key] {
			continue
		}
		ev := exp.Attributes[key]
		av := act.Attributes[key]
		if valuesEqual(ev, av) {
			continue
		}
		findings = append(findings, model.DriftFinding{
			Kind:         model.DriftAttributeChange,
			ResourceID:   exp.ID,
			ResourceType: exp.Type,
			ResourceName: exp.Name,
			Field:        key,
			Expected:     ev,
			Actual:       av,
			Severity:     model.SeverityWarning,
		})
	}
	return findings
}

func (e *Engine) diffTags(exp, act model.Resource) []model.DriftFinding {
	var findings []model.DriftFinding

	expTags := filterTags(exp.Tags, e.IgnoreTags)
	actTags := filterTags(act.Tags, e.IgnoreTags)

	allKeys := make(map[string]bool)
	for k := range expTags {
		allKeys[k] = true
	}
	for k := range actTags {
		allKeys[k] = true
	}

	for key := range allKeys {
		ev, eok := expTags[key]
		av, aok := actTags[key]
		if eok && aok && ev == av {
			continue
		}
		if !eok && aok {
			findings = append(findings, model.DriftFinding{
				Kind:         model.DriftTagsChanged,
				ResourceID:   exp.ID,
				ResourceType: exp.Type,
				ResourceName: exp.Name,
				Field:        fmt.Sprintf("tags.%s", key),
				Expected:     nil,
				Actual:       av,
				Severity:     model.SeverityInfo,
			})
			continue
		}
		if eok && !aok {
			findings = append(findings, model.DriftFinding{
				Kind:         model.DriftTagsChanged,
				ResourceID:   exp.ID,
				ResourceType: exp.Type,
				ResourceName: exp.Name,
				Field:        fmt.Sprintf("tags.%s", key),
				Expected:     ev,
				Actual:       nil,
				Severity:     model.SeverityInfo,
			})
			continue
		}
		if ev != av {
			findings = append(findings, model.DriftFinding{
				Kind:         model.DriftTagsChanged,
				ResourceID:   exp.ID,
				ResourceType: exp.Type,
				ResourceName: exp.Name,
				Field:        fmt.Sprintf("tags.%s", key),
				Expected:     ev,
				Actual:       av,
				Severity:     model.SeverityInfo,
			})
		}
	}
	return findings
}

func filterTags(tags map[string]string, ignore map[string]bool) map[string]string {
	out := make(map[string]string)
	for k, v := range tags {
		if !ignore[k] {
			out[k] = v
		}
	}
	return out
}

func valuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// JSON round-trip normalizes numeric types and nested structures
	aj, err1 := json.Marshal(normalizeForCompare(a))
	bj, err2 := json.Marshal(normalizeForCompare(b))
	if err1 == nil && err2 == nil {
		return string(aj) == string(bj)
	}

	return reflect.DeepEqual(a, b)
}

func normalizeForCompare(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = normalizeForCompare(item)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = normalizeForCompare(item)
		}
		return out
	case float64:
		if t == float64(int64(t)) {
			return int64(t)
		}
		return t
	default:
		return v
	}
}

func severityForMissing(r model.Resource) string {
	if env, ok := r.Tags["env"]; ok {
		if strings.EqualFold(env, "prod") || strings.EqualFold(env, "production") {
			return model.SeverityCritical
		}
	}
	return model.SeverityCritical
}

// BuildSummary computes summary statistics from findings and resource counts.
func BuildSummary(expectedCount int, findings []model.DriftFinding) model.DriftSummary {
	s := model.DriftSummary{
		TotalResources: expectedCount,
		TotalFindings:  len(findings),
	}
	for _, f := range findings {
		switch f.Kind {
		case model.DriftMissingInCloud:
			s.MissingInCloud++
		case model.DriftAttributeChange:
			s.AttributeChanges++
		case model.DriftTagsChanged:
			s.TagChanges++
		}
	}
	return s
}
