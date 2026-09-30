package drift

import (
	"testing"

	"github.com/driftctl/driftctl/internal/model"
)

func TestEngineCompareAttributesAndTagsIgnoresCloudOnlyResources(t *testing.T) {
	engine := NewEngine(model.CompareConfig{})

	expected := []model.Resource{
		{
			ID: "azure/azurerm_linux_virtual_machine/vm-123", Provider: "azure", Type: "azurerm_linux_virtual_machine",
			CloudID: "vm-123", Name: "web",
			Attributes: map[string]any{"size": "Standard_B1s"},
			Tags:       map[string]string{"env": "prod"},
		},
	}

	actual := []model.Resource{
		{
			ID: "azure/azurerm_linux_virtual_machine/vm-123", Provider: "azure", Type: "azurerm_linux_virtual_machine",
			CloudID: "vm-123", Name: "web",
			Attributes: map[string]any{"size": "Standard_B2s"},
			Tags:       map[string]string{"env": "staging"},
		},
		{
			ID: "azure/azurerm_resource_group/cloud-only", Provider: "azure", Type: "azurerm_resource_group",
			CloudID: "cloud-only", Name: "cloud-only",
		},
	}

	findings := engine.Compare(expected, actual)
	if len(findings) != 2 {
		t.Fatalf("expected only attribute and tag findings, got %+v", findings)
	}

	summary := BuildSummary(len(expected), findings)
	if summary.AttributeChanges < 1 {
		t.Fatal("expected attribute change")
	}
	if summary.TagChanges < 1 {
		t.Fatal("expected tag change")
	}
}

func TestEngineCompareMissingInCloud(t *testing.T) {
	engine := NewEngine(model.CompareConfig{})
	expected := []model.Resource{
		{ID: "azure/azurerm_linux_virtual_machine/vm-gone", Provider: "azure", Type: "azurerm_linux_virtual_machine", CloudID: "vm-gone", Tags: map[string]string{"env": "prod"}},
	}
	findings := engine.Compare(expected, nil)
	if len(findings) != 1 || findings[0].Kind != model.DriftMissingInCloud {
		t.Fatalf("expected missing finding, got %+v", findings)
	}
	if findings[0].Severity != model.SeverityCritical {
		t.Fatalf("expected critical severity, got %s", findings[0].Severity)
	}
}
