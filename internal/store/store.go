package store

import (
	"context"

	"github.com/driftctl/driftctl/internal/model"
)

// Store persists workspaces and scans.
type Store interface {
	Close() error

	SaveWorkspace(ctx context.Context, ws *model.Workspace) error
	GetWorkspaceByName(ctx context.Context, name string) (*model.Workspace, error)
	ListWorkspaces(ctx context.Context) ([]model.Workspace, error)

	SaveScan(ctx context.Context, report *model.DriftReport) error
	GetScan(ctx context.Context, id string) (*model.DriftReport, error)
}
