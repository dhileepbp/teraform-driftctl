package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/driftctl/driftctl/internal/config"
	"github.com/driftctl/driftctl/internal/model"
	"github.com/driftctl/driftctl/internal/notify"
	"github.com/driftctl/driftctl/internal/output"
	"github.com/driftctl/driftctl/internal/scan"
	"github.com/driftctl/driftctl/internal/store"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func newRootCmd() *cobra.Command {
	var cfgFile string
	root := &cobra.Command{
		Use:           "tfdriftctl",
		Short:         "Terraform drift detection CLI",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "path to driftctl.yaml")
	root.AddCommand(newScanCmd(&cfgFile))
	root.AddCommand(newReportCmd(&cfgFile))
	root.AddCommand(newWorkspaceCmd(&cfgFile))
	root.AddCommand(newScheduleCmd(&cfgFile))
	return root
}

type slackFlags struct {
	channels   []string
	tokenEnv   string
	webhookEnv string
	notifyOn   []string
}

func (f *slackFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringSliceVar(&f.channels, "slack-channel", nil, "Slack channel ID or name (repeatable; enables Slack)")
	cmd.Flags().StringVar(&f.tokenEnv, "slack-token-env", "", "environment variable containing a Slack bot token")
	cmd.Flags().StringVar(&f.webhookEnv, "slack-webhook-env", "", "environment variable containing a Slack webhook URL")
	cmd.Flags().StringSliceVar(&f.notifyOn, "slack-notify-on", nil, "events to notify: drift,error,clean,always")
}

func buildNotifiers(cfg *config.File, flags slackFlags) ([]notify.Notifier, error) {
	var slackCfg notify.SlackConfig
	if cfg != nil {
		slackCfg = cfg.Notifications.Slack
	}
	if len(flags.channels) > 0 {
		slackCfg.Enabled = true
		slackCfg.Channels = flags.channels
	}
	if flags.tokenEnv != "" {
		slackCfg.Enabled = true
		slackCfg.BotTokenEnv = flags.tokenEnv
	}
	if flags.webhookEnv != "" {
		slackCfg.Enabled = true
		slackCfg.WebhookEnv = flags.webhookEnv
	}
	if len(flags.notifyOn) > 0 {
		slackCfg.NotifyOn = flags.notifyOn
	}
	if !slackCfg.Enabled {
		return nil, nil
	}
	if slackCfg.BotTokenEnv == "" && slackCfg.WebhookEnv == "" {
		if len(slackCfg.Channels) > 0 {
			slackCfg.BotTokenEnv = "SLACK_BOT_TOKEN"
		} else {
			slackCfg.WebhookEnv = "SLACK_WEBHOOK_URL"
		}
	}
	slackNotifier, err := notify.NewSlack(slackCfg)
	if err != nil {
		return nil, err
	}
	return []notify.Notifier{slackNotifier}, nil
}

func openStore(cfgFile string) (store.Store, *config.File, error) {
	dbPath := "driftctl.db"
	var cfg *config.File
	if cfgFile != "" {
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return nil, nil, err
		}
		if cfg.Database != "" {
			dbPath = cfg.Database
		}
	}
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		return nil, nil, err
	}
	return st, cfg, nil
}

func newScanCmd(cfgFile *string) *cobra.Command {
	var (
		workspace     string
		statePath     string
		provider      string
		regions       []string
		subscriptions []string
		outputFmt     string
		jsonFile      string
		skipCloud     bool
		stateBucket   string
		stateKey      string
		stateRegion   string
		slack         slackFlags
	)

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Run a drift scan",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, cfg, err := openStore(*cfgFile)
			if err != nil {
				return err
			}
			defer st.Close()

			if cfg != nil {
				for i := range cfg.Workspaces {
					if err := st.SaveWorkspace(cmd.Context(), &cfg.Workspaces[i]); err != nil {
						return err
					}
				}
			}

			notifiers, err := buildNotifiers(cfg, slack)
			if err != nil {
				return fmt.Errorf("configure notifications: %w", err)
			}
			scanner := scan.NewScanner(st, notifiers...)
			opts := scan.Options{Compare: model.CompareConfig{}, Regions: regions}

			if workspace != "" {
				ws, err := st.GetWorkspaceByName(cmd.Context(), workspace)
				if err != nil {
					return fmt.Errorf("workspace %q: %w", workspace, err)
				}
				opts.WorkspaceID = ws.ID
				opts.WorkspaceName = ws.Name
				opts.Provider = ws.Provider
				opts.State = ws.State
				opts.Regions = ws.Regions
				opts.Subscriptions = ws.Subscriptions
				opts.Compare = ws.Compare
			} else {
				if statePath == "" && stateBucket == "" {
					return fmt.Errorf("either --workspace, --state, or --state-bucket is required")
				}
				if provider == "" {
					provider = "aws"
				}
				opts.WorkspaceName = "adhoc"
				opts.Provider = provider
				if stateBucket != "" {
					opts.State = model.StateConfig{
						Backend: "s3",
						Bucket:  stateBucket,
						Key:     stateKey,
						Region:  stateRegion,
					}
				} else {
					opts.State = model.StateConfig{Backend: "local", Path: statePath}
				}
			}
			opts.Subscriptions = append(opts.Subscriptions, subscriptions...)
			opts.SkipCloud = skipCloud

			report, err := scanner.Run(cmd.Context(), opts)
			if report == nil {
				return err
			}
			if err := output.Format(os.Stdout, report, outputFmt); err != nil {
				return err
			}
			if jsonFile != "" {
				if err := output.WriteJSONFile(jsonFile, report); err != nil {
					return err
				}
			}
			if err != nil {
				return err
			}
			if output.HasDrift(report) {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&workspace, "workspace", "", "workspace name")
	cmd.Flags().StringVar(&statePath, "state", "", "path to terraform.tfstate")
	cmd.Flags().StringVar(&provider, "provider", "aws", "cloud provider")
	cmd.Flags().StringSliceVar(&regions, "region", nil, "cloud regions (repeatable)")
	cmd.Flags().StringSliceVar(&subscriptions, "subscription", nil, "Azure subscription ID (repeatable)")
	cmd.Flags().StringVar(&outputFmt, "output", "table", "output format: json|table")
	cmd.Flags().StringVar(&jsonFile, "json-file", "", "also write the complete report to a JSON file")
	cmd.Flags().BoolVar(&skipCloud, "skip-cloud", false, "skip cloud fetch (state-only, for testing)")
	cmd.Flags().StringVar(&stateBucket, "state-bucket", "", "S3 bucket for terraform state")
	cmd.Flags().StringVar(&stateKey, "state-key", "", "S3 key for terraform state")
	cmd.Flags().StringVar(&stateRegion, "state-region", "", "S3 region for terraform state")
	slack.bind(cmd)
	return cmd
}

func newReportCmd(cfgFile *string) *cobra.Command {
	var outputFmt, jsonFile string
	cmd := &cobra.Command{
		Use:   "report [scan-id]",
		Short: "Show a drift report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openStore(*cfgFile)
			if err != nil {
				return err
			}
			defer st.Close()
			report, err := st.GetScan(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := output.Format(os.Stdout, report, outputFmt); err != nil {
				return err
			}
			if jsonFile != "" {
				return output.WriteJSONFile(jsonFile, report)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outputFmt, "output", "table", "output format: json|table")
	cmd.Flags().StringVar(&jsonFile, "json-file", "", "also write the complete report to a JSON file")
	return cmd
}

func newWorkspaceCmd(cfgFile *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage workspaces",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listWorkspaces(cmd.Context(), *cfgFile)
		},
	})
	return cmd
}

func listWorkspaces(ctx context.Context, cfgFile string) error {
	st, _, err := openStore(cfgFile)
	if err != nil {
		return err
	}
	defer st.Close()
	list, err := st.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func newScheduleCmd(cfgFile *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Manage scan schedules",
	}
	var cronExpr, workspace string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create or update a schedule",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openStore(*cfgFile)
			if err != nil {
				return err
			}
			defer st.Close()
			ws, err := st.GetWorkspaceByName(cmd.Context(), workspace)
			if err != nil {
				return err
			}
			return st.SaveSchedule(cmd.Context(), ws.ID, cronExpr)
		},
	}
	create.Flags().StringVar(&workspace, "workspace", "", "workspace name")
	create.Flags().StringVar(&cronExpr, "cron", "", "cron expression")
	_ = create.MarkFlagRequired("workspace")
	_ = create.MarkFlagRequired("cron")
	cmd.AddCommand(create)
	return cmd
}
