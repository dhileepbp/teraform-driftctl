package scan

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/driftctl/driftctl/internal/drift"
	"github.com/driftctl/driftctl/internal/model"
	"github.com/driftctl/driftctl/internal/notify"
	"github.com/driftctl/driftctl/internal/providers"
	"github.com/driftctl/driftctl/internal/providers/providerconfig"
	"github.com/driftctl/driftctl/internal/state"
	"github.com/driftctl/driftctl/internal/store"
)

// Scanner orchestrates drift detection scans.
type Scanner struct {
	StateReader *state.DefaultReader
	Extractor   *state.Extractor
	Providers   *providers.Registry
	Store       store.Store
	Notifiers   []notify.Notifier
}

// Options configures a single scan run.
type Options struct {
	WorkspaceID   string
	WorkspaceName string
	Provider      string
	State         model.StateConfig
	Regions       []string
	Subscriptions []string
	Compare       model.CompareConfig
	SkipCloud     bool // for testing with state-only
}

func NewScanner(st store.Store, notifiers ...notify.Notifier) *Scanner {
	return &Scanner{
		StateReader: state.NewReader(),
		Providers:   providers.DefaultRegistry(),
		Store:       st,
		Notifiers:   notifiers,
	}
}

// Run executes a full drift scan pipeline.
func (s *Scanner) Run(ctx context.Context, opts Options) (*model.DriftReport, error) {
	scanID := uuid.New().String()
	started := time.Now().UTC()

	report := &model.DriftReport{
		ScanID:      scanID,
		WorkspaceID: opts.WorkspaceID,
		Workspace:   opts.WorkspaceName,
		StartedAt:   started,
		Status:      model.ScanStatusRunning,
	}

	if err := s.Store.SaveScan(ctx, report); err != nil {
		return nil, fmt.Errorf("save scan: %w", err)
	}

	providerName := normalizeProviderName(opts.Provider)
	extractor := state.NewExtractor(providerName)
	stateData, err := s.StateReader.Read(ctx, opts.State)
	if err != nil {
		return s.failScan(ctx, report, fmt.Errorf("read state: %w", err))
	}

	expected, err := extractor.Extract(stateData)
	if err != nil {
		return s.failScan(ctx, report, fmt.Errorf("extract state: %w", err))
	}

	var actual []model.Resource

	if !opts.SkipCloud {
		provider, ok := s.Providers.Get(providerName)
		if !ok {
			return s.failScan(ctx, report, fmt.Errorf("unsupported provider: %s", opts.Provider))
		}
		actual, err = provider.FetchResources(ctx, expected, providerconfig.FetchOptions{
			Regions:       opts.Regions,
			Subscriptions: opts.Subscriptions,
		})
		if err != nil {
			return s.failScan(ctx, report, fmt.Errorf("fetch live %s resources: %w", providerName, err))
		}
	}

	engine := drift.NewEngine(opts.Compare)
	findings := engine.Compare(expected, actual)

	report.CompletedAt = time.Now().UTC()
	report.Status = model.ScanStatusCompleted
	report.Summary = drift.BuildSummary(len(expected), findings)
	report.Findings = findings

	if err := s.Store.SaveScan(ctx, report); err != nil {
		return nil, fmt.Errorf("save completed scan: %w", err)
	}
	s.notify(ctx, report)

	return report, nil
}

func normalizeProviderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "azurerm" {
		return "azure"
	}
	return name
}

func (s *Scanner) failScan(ctx context.Context, report *model.DriftReport, err error) (*model.DriftReport, error) {
	report.CompletedAt = time.Now().UTC()
	report.Status = model.ScanStatusFailed
	report.Errors = append(report.Errors, err.Error())
	_ = s.Store.SaveScan(ctx, report)
	s.notify(ctx, report)
	return report, err
}

func (s *Scanner) notify(ctx context.Context, report *model.DriftReport) {
	if len(s.Notifiers) == 0 {
		return
	}
	for _, notifier := range s.Notifiers {
		if err := notifier.Notify(ctx, report); err != nil {
			message := fmt.Sprintf("notification failed: %v", err)
			report.Errors = append(report.Errors, message)
			log.Printf("scan %s: %s", report.ScanID, message)
		}
	}
	// Persist notification errors without making delivery failure change drift results.
	_ = s.Store.SaveScan(ctx, report)
}
