package azure

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/driftctl/driftctl/internal/model"
	"github.com/driftctl/driftctl/internal/providers/providerconfig"
)

func TestProviderFetchResources(t *testing.T) {
	credential := &staticCredential{}
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		var query graphQueryRequest
		if err := json.NewDecoder(req.Body).Decode(&query); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(query.Subscriptions, []string{"sub-123"}) {
			t.Fatalf("unexpected subscriptions: %v", query.Subscriptions)
		}

		body := `{"data":[]}`
		if strings.HasPrefix(query.Query, "Resources ") {
			body = `{"data":[
  {
    "id":"/SUBSCRIPTIONS/sub-123/resourceGroups/prod/providers/Microsoft.Storage/storageAccounts/appdata",
    "name":"appdata",
    "type":"microsoft.storage/storageaccounts",
    "location":"eastus",
    "tags":{"env":"prod"},
    "kind":"StorageV2",
    "sku":{"name":"Standard_LRS"},
    "properties":{"accessTier":"Hot","minimumTlsVersion":"TLS1_2","supportsHttpsTrafficOnly":true}
  },
  {
    "id":"/subscriptions/sub-123/resourceGroups/prod/providers/Microsoft.KeyVault/vaults/unmanaged",
    "name":"unmanaged",
    "type":"microsoft.keyvault/vaults",
    "location":"eastus"
  }
]}`
		}
		return response(http.StatusOK, body), nil
	})}

	resourceID := "/subscriptions/sub-123/resourcegroups/prod/providers/microsoft.storage/storageaccounts/appdata"
	expected := []model.Resource{{
		ID:       "azure/azurerm_storage_account/" + resourceID,
		Provider: "azure",
		Type:     "azurerm_storage_account",
		CloudID:  resourceID,
	}}
	provider := &Provider{
		newCredential: func() (azcore.TokenCredential, error) { return credential, nil },
		httpClient:    client,
		endpoint:      "https://resourcegraph.test/resources",
	}

	actual, err := provider.FetchResources(context.Background(), expected, providerconfig.FetchOptions{
		Subscriptions: []string{"sub-123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("expected resource and resource-group queries, got %d requests", requests)
	}
	if len(actual) != 2 {
		t.Fatalf("expected two resources, got %d", len(actual))
	}
	if actual[0].ID != expected[0].ID {
		t.Fatalf("expected Terraform resource ID %q, got %q", expected[0].ID, actual[0].ID)
	}
	if got := actual[0].Attributes["account_replication_type"]; got != "LRS" {
		t.Fatalf("expected LRS replication, got %v", got)
	}
	if actual[1].Type != "azurerm_key_vault" {
		t.Fatalf("expected mapped Key Vault type, got %q", actual[1].Type)
	}
	if !reflect.DeepEqual(credential.scopes, []string{resourceManagerScope}) {
		t.Fatalf("unexpected token scopes: %v", credential.scopes)
	}
}

func TestResolveSubscriptionsFromResourceIDs(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	t.Setenv("AZURESUBSCRIPTION_SUBSCRIPTION_ID", "")
	resources := []model.Resource{{CloudID: "/subscriptions/from-state/resourceGroups/example"}}
	got := resolveSubscriptions([]string{"explicit", "explicit"}, resources)
	want := []string{"explicit", "from-state"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNewCredentialRejectsIncompleteAzurePipelinesEnvironment(t *testing.T) {
	for _, name := range []string{
		"AZURESUBSCRIPTION_TENANT_ID",
		"AZURESUBSCRIPTION_CLIENT_ID",
		"AZURESUBSCRIPTION_SERVICE_CONNECTION_ID",
		"SYSTEM_ACCESSTOKEN",
		"SYSTEM_OIDCREQUESTURI",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("AZURESUBSCRIPTION_CLIENT_ID", "client-id")
	_, err := newCredential()
	if err == nil || !strings.Contains(err.Error(), "SYSTEM_ACCESSTOKEN") {
		t.Fatalf("expected missing OIDC environment error, got %v", err)
	}
}

func TestNewCredentialSelectsAzurePipelinesCredential(t *testing.T) {
	t.Setenv("AZURESUBSCRIPTION_TENANT_ID", "11111111-1111-1111-1111-111111111111")
	t.Setenv("AZURESUBSCRIPTION_CLIENT_ID", "22222222-2222-2222-2222-222222222222")
	t.Setenv("AZURESUBSCRIPTION_SERVICE_CONNECTION_ID", "33333333-3333-3333-3333-333333333333")
	t.Setenv("SYSTEM_ACCESSTOKEN", "system-token")
	t.Setenv("SYSTEM_OIDCREQUESTURI", "https://dev.azure.test/oidc")
	credential, err := newCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := credential.(*azidentity.AzurePipelinesCredential); !ok {
		t.Fatalf("expected AzurePipelinesCredential, got %T", credential)
	}
}

type staticCredential struct {
	scopes []string
}

func (c *staticCredential) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.scopes = options.Scopes
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
