package state

import (
	"os"
	"testing"
)

func TestExtractorExtract(t *testing.T) {
	data, err := os.ReadFile("../../testdata/state/azure-sample.tfstate")
	if err != nil {
		t.Fatal(err)
	}
	e := NewExtractor("azure")
	resources, err := e.Extract(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 3 {
		t.Fatalf("expected 3 resources, got %d", len(resources))
	}

	byType := make(map[string]int)
	for _, r := range resources {
		byType[r.Type]++
		if r.Provider != "azure" {
			t.Fatalf("expected azure provider, got %s", r.Provider)
		}
		if r.Source != "state" {
			t.Fatalf("expected state source, got %s", r.Source)
		}
	}
	if byType["azurerm_resource_group"] != 1 || byType["azurerm_storage_account"] != 1 || byType["azurerm_virtual_network"] != 1 {
		t.Fatalf("unexpected resource types: %v", byType)
	}
}

func TestExtractorNormalizesAzureResourceIDs(t *testing.T) {
	data := []byte(`{
  "version": 4,
  "resources": [{
    "mode": "managed",
    "type": "azurerm_resource_group",
    "name": "prod",
    "provider": "provider[\"registry.terraform.io/hashicorp/azurerm\"]",
    "instances": [{"attributes": {
      "id": "/SUBSCRIPTIONS/ABC/resourceGroups/Prod/",
      "name": "Prod",
      "location": "eastus",
      "tags": {"env": "prod"}
    }}]
  }]
}`)
	resources, err := NewExtractor("azure").Extract(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected one resource, got %d", len(resources))
	}
	wantID := "/subscriptions/abc/resourcegroups/prod"
	if resources[0].CloudID != wantID {
		t.Fatalf("expected normalized Azure ID %q, got %q", wantID, resources[0].CloudID)
	}
	if resources[0].Attributes["location"] != "eastus" {
		t.Fatalf("expected location attribute, got %v", resources[0].Attributes)
	}
}

func TestAzureExtractorIgnoresNonAzureManagedResources(t *testing.T) {
	data := []byte(`{
  "version": 4,
  "resources": [
    {
      "mode": "managed",
      "type": "azurerm_resource_group",
      "name": "prod",
      "provider": "provider[\"registry.terraform.io/hashicorp/azurerm\"]",
      "instances": [{"attributes": {"id": "/subscriptions/sub/resourceGroups/prod", "name": "prod", "location": "eastus"}}]
    },
    {
      "mode": "managed",
      "type": "random_id",
      "name": "suffix",
      "provider": "provider[\"registry.terraform.io/hashicorp/random\"]",
      "instances": [{"attributes": {"id": "not-an-arm-id"}}]
    }
  ]
}`)

	resources, err := NewExtractor("azure").Extract(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Type != "azurerm_resource_group" {
		t.Fatalf("expected only the Azure resource, got %+v", resources)
	}
}
