package model

import "time"

// Resource is the canonical normalized representation of cloud infrastructure.
type Resource struct {
	ID         string            `json:"id"`
	Provider   string            `json:"provider"`
	Type       string            `json:"type"`
	CloudID    string            `json:"cloud_id"`
	Name       string            `json:"name"`
	Attributes map[string]any    `json:"attributes"`
	Tags       map[string]string `json:"tags"`
	Region     string            `json:"region"`
	Source     string            `json:"source"`
	Module     string            `json:"module,omitempty"`
}

// DriftKind categorizes a drift finding.
type DriftKind string

const (
	DriftMissingInCloud  DriftKind = "missing_in_cloud"
	DriftAttributeChange DriftKind = "attribute_changed"
	DriftTagsChanged     DriftKind = "tags_changed"
)

// Severity levels for drift findings.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// DriftFinding represents a single detected drift item.
type DriftFinding struct {
	Kind         DriftKind `json:"kind"`
	ResourceID   string    `json:"resource_id"`
	ResourceType string    `json:"resource_type"`
	ResourceName string    `json:"resource_name,omitempty"`
	Field        string    `json:"field,omitempty"`
	Expected     any       `json:"expected,omitempty"`
	Actual       any       `json:"actual,omitempty"`
	Severity     string    `json:"severity"`
}

// DriftSummary aggregates finding counts.
type DriftSummary struct {
	TotalResources   int `json:"total_resources"`
	MissingInCloud   int `json:"missing_in_cloud"`
	AttributeChanges int `json:"attribute_changes"`
	TagChanges       int `json:"tag_changes"`
	TotalFindings    int `json:"total_findings"`
}

// DriftReport is the output of a drift scan.
type DriftReport struct {
	ScanID      string         `json:"scan_id"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	Workspace   string         `json:"workspace,omitempty"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt time.Time      `json:"completed_at"`
	Status      string         `json:"status"`
	Summary     DriftSummary   `json:"summary"`
	Findings    []DriftFinding `json:"findings"`
	Errors      []string       `json:"errors,omitempty"`
}

// HasDrift reports whether the scan found actionable infrastructure drift.
func (r *DriftReport) HasDrift() bool {
	return r != nil && r.Summary.TotalFindings > 0
}

// ScanStatus values.
const (
	ScanStatusRunning   = "running"
	ScanStatusCompleted = "completed"
	ScanStatusFailed    = "failed"
)

// Workspace represents a configured drift detection target.
type Workspace struct {
	ID            string        `json:"id" yaml:"id,omitempty"`
	Name          string        `json:"name" yaml:"name"`
	Provider      string        `json:"provider" yaml:"provider"`
	State         StateConfig   `json:"state" yaml:"state"`
	Subscriptions []string      `json:"subscriptions,omitempty" yaml:"subscriptions,omitempty"`
	Compare       CompareConfig `json:"compare" yaml:"compare,omitempty"`
}

// StateConfig describes where Terraform state is stored.
type StateConfig struct {
	Backend string            `json:"backend" yaml:"backend"`
	Path    string            `json:"path,omitempty" yaml:"path,omitempty"`
	Extra   map[string]string `json:"extra,omitempty" yaml:"extra,omitempty"`
}

// CompareConfig controls drift comparison behavior.
type CompareConfig struct {
	IgnoreTags       []string `json:"ignore_tags" yaml:"ignore_tags"`
	IgnoreAttributes []string `json:"ignore_attributes" yaml:"ignore_attributes"`
}

// ResourceSelector targets specific resources for cloud fetch.
type ResourceSelector struct {
	Type    string
	Region  string
	CloudID string
}
