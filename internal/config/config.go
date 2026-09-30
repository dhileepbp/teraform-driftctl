package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/driftctl/driftctl/internal/model"
	"github.com/driftctl/driftctl/internal/notify"
)

// File represents the driftctl.yaml configuration.
type File struct {
	Database      string              `yaml:"database"`
	Notifications NotificationsConfig `yaml:"notifications"`
	Workspaces    []model.Workspace   `yaml:"workspaces"`
}

type NotificationsConfig struct {
	Slack notify.SlackConfig `yaml:"slack"`
}

// Load reads configuration from a YAML file.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg File
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Database == "" {
		cfg.Database = "driftctl.db"
	}
	return &cfg, nil
}
