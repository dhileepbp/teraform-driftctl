package notify

import (
	"context"

	"github.com/driftctl/driftctl/internal/model"
)

// Notifier publishes completed drift reports to an external destination.
type Notifier interface {
	Notify(context.Context, *model.DriftReport) error
}
