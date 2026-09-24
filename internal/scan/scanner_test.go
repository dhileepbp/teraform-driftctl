package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/driftctl/driftctl/internal/model"
	"github.com/driftctl/driftctl/internal/providers/providerconfig"
	"github.com/driftctl/driftctl/internal/store"
)

func TestScannerRunSkipCloud(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	statePath, _ := filepath.Abs("../../testdata/state/sample.tfstate")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(st)
	report, err := scanner.Run(context.Background(), Options{
		WorkspaceName: "test",
		Provider:      "aws",
		State:         model.StateConfig{Backend: "local", Path: statePath},
		SkipCloud:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != model.ScanStatusCompleted {
		t.Fatalf("expected completed, got %s", report.Status)
	}
	if report.Summary.TotalResources != 3 {
		t.Fatalf("expected 3 resources, got %d", report.Summary.TotalResources)
	}
	// Without cloud data, all resources appear missing
	if report.Summary.MissingInCloud != 3 {
		t.Fatalf("expected 3 missing, got %d", report.Summary.MissingInCloud)
	}
}

func TestNormalizeProviderName(t *testing.T) {
	if got := normalizeProviderName("AzureRM"); got != "azure" {
		t.Fatalf("expected azure alias, got %q", got)
	}
}

func TestScannerFailsWhenLiveDiscoveryFails(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	statePath, err := filepath.Abs("../../testdata/state/sample.tfstate")
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(st)
	scanner.Providers.Register(failingProvider{})
	report, err := scanner.Run(context.Background(), Options{
		WorkspaceName: "azure-test",
		Provider:      "azure",
		State:         model.StateConfig{Backend: "local", Path: statePath},
		Subscriptions: []string{"sub-123"},
	})
	if err == nil {
		t.Fatal("expected live discovery error")
	}
	if report == nil || report.Status != model.ScanStatusFailed {
		t.Fatalf("expected failed report, got %+v", report)
	}
}

type failingProvider struct{}

func (failingProvider) Name() string { return "azure" }

func (failingProvider) SupportedTypes() []string { return nil }

func (failingProvider) FetchResources(context.Context, []model.Resource, providerconfig.FetchOptions) ([]model.Resource, error) {
	return nil, errors.New("discovery unavailable")
}
