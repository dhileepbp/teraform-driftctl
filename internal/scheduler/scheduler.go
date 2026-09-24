package scheduler

import (
	"context"
	"log"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/driftctl/driftctl/internal/model"
	"github.com/driftctl/driftctl/internal/scan"
	"github.com/driftctl/driftctl/internal/store"
)

// Scheduler runs workspace scans on cron schedules.
type Scheduler struct {
	cron    *cron.Cron
	scanner *scan.Scanner
	store   store.Store
	mu      sync.Mutex
	entries map[string]cron.EntryID
}

func New(scanner *scan.Scanner, st store.Store) *Scheduler {
	return &Scheduler{
		cron:    cron.New(),
		scanner: scanner,
		store:   st,
		entries: make(map[string]cron.EntryID),
	}
}

func (s *Scheduler) Start() {
	s.cron.Start()
}

func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

func (s *Scheduler) LoadFromStore(ctx context.Context) error {
	schedules, err := s.store.ListSchedules(ctx)
	if err != nil {
		return err
	}
	for wsID, cronExpr := range schedules {
		if err := s.Register(ctx, wsID, cronExpr); err != nil {
			log.Printf("scheduler: skip workspace %s: %v", wsID, err)
		}
	}
	return nil
}

func (s *Scheduler) Register(ctx context.Context, workspaceID, cronExpr string) error {
	ws, err := s.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	return s.registerWorkspace(ws, cronExpr)
}

func (s *Scheduler) registerWorkspace(ws *model.Workspace, cronExpr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.entries[ws.ID]; ok {
		s.cron.Remove(existing)
	}

	wsCopy := *ws
	id, err := s.cron.AddFunc(cronExpr, func() {
		_, err := s.scanner.Run(context.Background(), scan.Options{
			WorkspaceID:   wsCopy.ID,
			WorkspaceName: wsCopy.Name,
			Provider:      wsCopy.Provider,
			State:         wsCopy.State,
			Regions:       wsCopy.Regions,
			Subscriptions: wsCopy.Subscriptions,
			Compare:       wsCopy.Compare,
		})
		if err != nil {
			log.Printf("scheduled scan failed for %s: %v", wsCopy.Name, err)
		}
	})
	if err != nil {
		return err
	}
	s.entries[ws.ID] = id
	return nil
}

func (s *Scheduler) Unregister(workspaceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.entries[workspaceID]; ok {
		s.cron.Remove(id)
		delete(s.entries, workspaceID)
	}
}
