package state

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/driftctl/driftctl/internal/model"
)

type tfState struct {
	Version          int          `json:"version"`
	TerraformVersion string       `json:"terraform_version"`
	Resources        []tfResource `json:"resources"`
}

type tfResource struct {
	Module    string       `json:"module"`
	Mode      string       `json:"mode"`
	Type      string       `json:"type"`
	Name      string       `json:"name"`
	Provider  string       `json:"provider"`
	Instances []tfInstance `json:"instances"`
}

type tfInstance struct {
	Attributes map[string]any `json:"attributes"`
}

// Extractor parses Terraform state JSON into normalized resources.
type Extractor struct {
	Provider string
}

func NewExtractor(provider string) *Extractor {
	return &Extractor{Provider: provider}
}

func (e *Extractor) Extract(data []byte) ([]model.Resource, error) {
	var state tfState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state JSON: %w", err)
	}

	var resources []model.Resource
	for _, res := range state.Resources {
		if res.Mode != "managed" {
			continue
		}
		for _, inst := range res.Instances {
			r, ok := e.extractResource(res, inst)
			if !ok {
				continue
			}
			resources = append(resources, r)
		}
	}
	return resources, nil
}

func (e *Extractor) extractResource(res tfResource, inst tfInstance) (model.Resource, bool) {
	attrs := inst.Attributes
	if attrs == nil {
		return model.Resource{}, false
	}

	inferredProvider := inferProvider(res.Provider, res.Type)
	provider := e.Provider
	if provider == "" {
		provider = inferredProvider
	}
	if provider == "azure" && inferredProvider != "azure" {
		return model.Resource{}, false
	}

	cloudID := resolveCloudID(res.Type, attrs)
	if cloudID == "" {
		return model.Resource{}, false
	}
	if provider == "azure" {
		cloudID = normalizeAzureResourceID(cloudID)
	}

	region := stringAttr(attrs, "location")

	name := stringAttr(attrs, "name", "id")
	tags := extractTags(attrs)

	normalized := normalizeAttributes(res.Type, attrs)

	id := fmt.Sprintf("%s/%s/%s", provider, res.Type, cloudID)

	return model.Resource{
		ID:         id,
		Provider:   provider,
		Type:       res.Type,
		CloudID:    cloudID,
		Name:       name,
		Attributes: normalized,
		Tags:       tags,
		Region:     region,
		Source:     "state",
		Module:     strings.TrimPrefix(res.Module, "module."),
	}, true
}

func normalizeAzureResourceID(id string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(id), "/"))
}

func resolveCloudID(_ string, attrs map[string]any) string {
	candidates := []string{"id"}
	for _, key := range candidates {
		if v := stringAttr(attrs, key); v != "" {
			return v
		}
	}
	return ""
}

func stringAttr(attrs map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := attrs[key]; ok && v != nil {
			switch t := v.(type) {
			case string:
				if t != "" {
					return t
				}
			case float64:
				return fmt.Sprintf("%v", t)
			case bool:
				return fmt.Sprintf("%v", t)
			}
		}
	}
	return ""
}

func extractTags(attrs map[string]any) map[string]string {
	tags := make(map[string]string)

	if raw, ok := attrs["tags"]; ok {
		switch t := raw.(type) {
		case map[string]any:
			for k, v := range t {
				tags[k] = fmt.Sprintf("%v", v)
			}
		case map[string]string:
			for k, v := range t {
				tags[k] = v
			}
		}
	}

	return tags
}

func inferProvider(providerField, resType string) string {
	if strings.Contains(providerField, "azurerm") || strings.HasPrefix(resType, "azurerm_") {
		return "azure"
	}
	return "unknown"
}

// terraformOnlyKeys are never compared for drift.
var terraformOnlyKeys = map[string]bool{
	"id": true, "tags": true, "timeouts": true,
}

func normalizeAttributes(resType string, attrs map[string]any) map[string]any {
	compareKeys := compareKeysForType(resType)
	out := make(map[string]any)

	for _, key := range compareKeys {
		if v, ok := attrs[key]; ok {
			out[key] = normalizeValue(v)
		}
	}
	return out
}

func compareKeysForType(resType string) []string {
	switch resType {
	case "azurerm_resource_group":
		return []string{"location"}
	case "azurerm_storage_account":
		return []string{"account_tier", "account_replication_type", "account_kind", "access_tier", "min_tls_version", "https_traffic_only_enabled"}
	case "azurerm_virtual_network":
		return []string{"address_space", "dns_servers"}
	case "azurerm_subnet":
		return []string{"address_prefixes", "network_security_group_id"}
	case "azurerm_linux_virtual_machine", "azurerm_windows_virtual_machine", "azurerm_virtual_machine":
		return []string{"size"}
	case "azurerm_public_ip":
		return []string{"allocation_method", "sku"}
	default:
		var keys []string
		for k := range attrsFromType(resType) {
			if !terraformOnlyKeys[k] {
				keys = append(keys, k)
			}
		}
		return keys
	}
}

func attrsFromType(_ string) map[string]bool {
	return map[string]bool{}
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = normalizeValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = normalizeValue(item)
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
