package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/driftctl/driftctl/internal/api"
	"github.com/driftctl/driftctl/internal/config"
	"github.com/driftctl/driftctl/internal/notify"
	"github.com/driftctl/driftctl/internal/scan"
	"github.com/driftctl/driftctl/internal/scheduler"
	"github.com/driftctl/driftctl/internal/store"
)

func main() {
	cfgPath := flag.String("config", "configs/driftctl.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	st, err := store.OpenSQLite(cfg.Database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	ctx := context.Background()
	for i := range cfg.Workspaces {
		if err := st.SaveWorkspace(ctx, &cfg.Workspaces[i]); err != nil {
			fmt.Fprintf(os.Stderr, "save workspace: %v\n", err)
			os.Exit(1)
		}
		if cfg.Workspaces[i].Schedule != nil && cfg.Workspaces[i].Schedule.Cron != "" {
			_ = st.SaveSchedule(ctx, cfg.Workspaces[i].ID, cfg.Workspaces[i].Schedule.Cron)
		}
	}

	var notifiers []notify.Notifier
	if cfg.Notifications.Slack.Enabled {
		slackNotifier, err := notify.NewSlack(cfg.Notifications.Slack)
		if err != nil {
			fmt.Fprintf(os.Stderr, "configure Slack: %v\n", err)
			os.Exit(1)
		}
		notifiers = append(notifiers, slackNotifier)
	}
	scanner := scan.NewScanner(st, notifiers...)
	sched := scheduler.New(scanner, st)
	if err := sched.LoadFromStore(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "load schedules: %v\n", err)
		os.Exit(1)
	}
	sched.Start()
	defer sched.Stop()

	srv := api.NewServer(cfg.API, st, scanner, sched)

	runCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := api.Run(runCtx, cfg.API.Addr, srv.Handler()); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
