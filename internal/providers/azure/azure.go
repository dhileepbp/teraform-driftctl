package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/driftctl/driftctl/internal/model"
	"github.com/driftctl/driftctl/internal/providers/providerconfig"
)

const (
	resourceGraphEndpoint = "https://management.azure.com/providers/Microsoft.ResourceGraph/resources?api-version=2024-04-01"
	resourceManagerScope  = "https://management.azure.com/.default"
)

type credentialFactory func() (azcore.TokenCredential, error)

// Provider implements Azure live discovery through Azure Resource Graph.
type Provider struct {
	newCredential credentialFactory
	httpClient    *http.Client
	endpoint      string
}

func NewProvider() *Provider {
	return &Provider{
		newCredential: newCredential,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		endpoint:      resourceGraphEndpoint,
	}
}

func (p *Provider) Name() string { return "azure" }

func (p *Provider) SupportedTypes() []string {
	return []string{
		"azurerm_resource_group",
		"azurerm_storage_account",
		"azurerm_virtual_network",
		"azurerm_subnet",
		"azurerm_network_security_group",
		"azurerm_linux_virtual_machine",
		"azurerm_windows_virtual_machine",
		"azurerm_virtual_machine",
		"azurerm_public_ip",
		"azurerm_key_vault",
		"azurerm_container_registry",
		"azurerm_kubernetes_cluster",
	}
}

func (p *Provider) FetchResources(ctx context.Context, expected []model.Resource, opts providerconfig.FetchOptions) ([]model.Resource, error) {
	if len(expected) == 0 {
		return nil, nil
	}
	subscriptions := resolveSubscriptions(opts.Subscriptions, expected)
	if len(subscriptions) == 0 {
		return nil, fmt.Errorf("Azure discovery requires --subscription, workspace subscriptions, or AZURE_SUBSCRIPTION_ID")
	}

	credential, err := p.newCredential()
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}

	var rows []graphResource
	for _, query := range stateScopedQueries(expected) {
		items, err := p.query(ctx, credential, subscriptions, query)
		if err != nil {
			return nil, err
		}
		rows = append(rows, items...)
	}

	expectedByCloudID := make(map[string]model.Resource, len(expected))
	for _, resource := range expected {
		expectedByCloudID[normalizeResourceID(resource.CloudID)] = resource
	}

	resources := make([]model.Resource, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		cloudID := normalizeResourceID(row.ID)
		if cloudID == "" || seen[cloudID] {
			continue
		}
		seen[cloudID] = true

		expectedResource, ok := expectedByCloudID[cloudID]
		if !ok {
			continue
		}

		resources = append(resources, model.Resource{
			ID:         expectedResource.ID,
			Provider:   "azure",
			Type:       expectedResource.Type,
			CloudID:    cloudID,
			Name:       row.Name,
			Attributes: azureAttributes(expectedResource.Type, row),
			Tags:       stringMap(row.Tags),
			Region:     strings.ToLower(row.Location),
			Source:     "cloud",
		})
	}

	return resources, nil
}

const resourceIDQueryChunkSize = 200

func stateScopedQueries(expected []model.Resource) []string {
	set := make(map[string]bool, len(expected))
	for _, resource := range expected {
		if id := normalizeResourceID(resource.CloudID); id != "" {
			set[id] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	queries := make([]string, 0, ((len(ids)+resourceIDQueryChunkSize-1)/resourceIDQueryChunkSize)*2)
	for start := 0; start < len(ids); start += resourceIDQueryChunkSize {
		end := start + resourceIDQueryChunkSize
		if end > len(ids) {
			end = len(ids)
		}
		quoted := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			quoted = append(quoted, "'"+strings.ReplaceAll(id, "'", "''")+"'")
		}
		filter := strings.Join(quoted, ",")
		queries = append(queries,
			"Resources | where id in~ ("+filter+") | project id, name, type, location, tags, properties, sku, kind, subscriptionId, resourceGroup",
			"ResourceContainers | where type =~ 'microsoft.resources/subscriptions/resourcegroups' | where id in~ ("+filter+") | project id, name, type, location, tags, subscriptionId",
		)
	}
	return queries
}

func newCredential() (azcore.TokenCredential, error) {
	pipelineTenantID := os.Getenv("AZURESUBSCRIPTION_TENANT_ID")
	pipelineClientID := os.Getenv("AZURESUBSCRIPTION_CLIENT_ID")
	serviceConnectionID := os.Getenv("AZURESUBSCRIPTION_SERVICE_CONNECTION_ID")
	systemAccessToken := os.Getenv("SYSTEM_ACCESSTOKEN")
	oidcRequestURI := os.Getenv("SYSTEM_OIDCREQUESTURI")

	if pipelineTenantID != "" || pipelineClientID != "" || serviceConnectionID != "" || systemAccessToken != "" || oidcRequestURI != "" {
		tenantID := firstEnvironment("AZURESUBSCRIPTION_TENANT_ID", "AZURE_TENANT_ID")
		clientID := firstEnvironment("AZURESUBSCRIPTION_CLIENT_ID", "AZURE_CLIENT_ID")
		missing := missingAzurePipelineValues(tenantID, clientID, serviceConnectionID, systemAccessToken, oidcRequestURI)
		if len(missing) > 0 {
			return nil, fmt.Errorf("incomplete Azure Pipelines OIDC environment; missing %s", strings.Join(missing, ", "))
		}
		return azidentity.NewAzurePipelinesCredential(tenantID, clientID, serviceConnectionID, systemAccessToken, nil)
	}

	return azidentity.NewDefaultAzureCredential(nil)
}

func missingAzurePipelineValues(tenantID, clientID, serviceConnectionID, systemAccessToken, oidcRequestURI string) []string {
	var missing []string
	if tenantID == "" {
		missing = append(missing, "AZURESUBSCRIPTION_TENANT_ID")
	}
	if clientID == "" {
		missing = append(missing, "AZURESUBSCRIPTION_CLIENT_ID")
	}
	if serviceConnectionID == "" {
		missing = append(missing, "AZURESUBSCRIPTION_SERVICE_CONNECTION_ID")
	}
	if systemAccessToken == "" {
		missing = append(missing, "SYSTEM_ACCESSTOKEN")
	}
	if oidcRequestURI == "" {
		missing = append(missing, "SYSTEM_OIDCREQUESTURI")
	}
	return missing
}

func firstEnvironment(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func resolveSubscriptions(configured []string, expected []model.Resource) []string {
	set := make(map[string]bool)
	add := func(value string) {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				set[item] = true
			}
		}
	}
	for _, subscription := range configured {
		add(subscription)
	}
	add(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	add(os.Getenv("AZURESUBSCRIPTION_SUBSCRIPTION_ID"))
	for _, resource := range expected {
		if subscription := subscriptionFromResourceID(resource.CloudID); subscription != "" {
			add(subscription)
		}
	}
	result := make([]string, 0, len(set))
	for subscription := range set {
		result = append(result, subscription)
	}
	sort.Strings(result)
	return result
}

func subscriptionFromResourceID(id string) string {
	parts := strings.Split(strings.Trim(id, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "subscriptions") {
			return parts[i+1]
		}
	}
	return ""
}

func normalizeResourceID(id string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(id), "/"))
}

type graphQueryRequest struct {
	Subscriptions []string          `json:"subscriptions"`
	Query         string            `json:"query"`
	Options       graphQueryOptions `json:"options"`
}

type graphQueryOptions struct {
	Top          int    `json:"$top,omitempty"`
	SkipToken    string `json:"$skipToken,omitempty"`
	ResultFormat string `json:"resultFormat"`
}

type graphQueryResponse struct {
	Data      []graphResource `json:"data"`
	SkipToken string          `json:"$skipToken"`
}

type graphResource struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Type           string         `json:"type"`
	Location       string         `json:"location"`
	SubscriptionID string         `json:"subscriptionId"`
	ResourceGroup  string         `json:"resourceGroup"`
	Kind           string         `json:"kind"`
	Tags           map[string]any `json:"tags"`
	Properties     map[string]any `json:"properties"`
	SKU            map[string]any `json:"sku"`
}

func (p *Provider) query(ctx context.Context, credential azcore.TokenCredential, subscriptions []string, query string) ([]graphResource, error) {
	var resources []graphResource
	var skipToken string
	for {
		requestBody := graphQueryRequest{
			Subscriptions: subscriptions,
			Query:         query,
			Options: graphQueryOptions{
				Top:          1000,
				SkipToken:    skipToken,
				ResultFormat: "objectArray",
			},
		}
		body, err := json.Marshal(requestBody)
		if err != nil {
			return nil, err
		}
		token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{resourceManagerScope}})
		if err != nil {
			return nil, fmt.Errorf("authenticate to Azure Resource Manager: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("query Azure Resource Graph: %w", err)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Azure Resource Graph response: %w", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("query Azure Resource Graph: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
		}
		var result graphQueryResponse
		if err := json.Unmarshal(responseBody, &result); err != nil {
			return nil, fmt.Errorf("decode Azure Resource Graph response: %w", err)
		}
		resources = append(resources, result.Data...)
		if result.SkipToken == "" {
			return resources, nil
		}
		if result.SkipToken == skipToken {
			return nil, fmt.Errorf("query Azure Resource Graph: repeated pagination token")
		}
		skipToken = result.SkipToken
	}
}

func azureAttributes(resourceType string, row graphResource) map[string]any {
	attributes := make(map[string]any)
	set := func(key string, value any) {
		if value != nil && value != "" {
			attributes[key] = value
		}
	}

	switch resourceType {
	case "azurerm_resource_group":
		set("location", strings.ToLower(row.Location))
	case "azurerm_storage_account":
		skuName := stringValue(row.SKU["name"])
		if parts := strings.SplitN(skuName, "_", 2); len(parts) == 2 {
			set("account_tier", parts[0])
			set("account_replication_type", parts[1])
		}
		set("account_kind", row.Kind)
		set("access_tier", nestedValue(row.Properties, "accessTier"))
		set("min_tls_version", nestedValue(row.Properties, "minimumTlsVersion"))
		set("https_traffic_only_enabled", nestedValue(row.Properties, "supportsHttpsTrafficOnly"))
	case "azurerm_virtual_network":
		set("address_space", nestedValue(row.Properties, "addressSpace", "addressPrefixes"))
		set("dns_servers", nestedValue(row.Properties, "dhcpOptions", "dnsServers"))
	case "azurerm_subnet":
		prefixes := nestedValue(row.Properties, "addressPrefixes")
		if prefixes == nil {
			if prefix := nestedValue(row.Properties, "addressPrefix"); prefix != nil {
				prefixes = []any{prefix}
			}
		}
		set("address_prefixes", prefixes)
		set("network_security_group_id", nestedValue(row.Properties, "networkSecurityGroup", "id"))
	case "azurerm_linux_virtual_machine", "azurerm_windows_virtual_machine", "azurerm_virtual_machine":
		set("size", nestedValue(row.Properties, "hardwareProfile", "vmSize"))
	case "azurerm_public_ip":
		set("allocation_method", nestedValue(row.Properties, "publicIPAllocationMethod"))
		set("sku", row.SKU["name"])
	}
	return attributes
}

func nestedValue(values map[string]any, path ...string) any {
	var current any = values
	for _, part := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func stringMap(values map[string]any) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = stringValue(value)
	}
	return result
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
