package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadSlackConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "driftctl.yaml")
	data := []byte(`
notifications:
  slack:
    enabled: true
    bot_token_env: TEST_SLACK_TOKEN
    channels: [C123, C456]
    notify_on: [drift, error]
    max_findings: 5
workspaces: []
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Notifications.Slack.Enabled {
		t.Fatal("expected Slack to be enabled")
	}
	if got, want := cfg.Notifications.Slack.Channels, []string{"C123", "C456"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected channels %v, got %v", want, got)
	}
	if cfg.Notifications.Slack.MaxFindings != 5 {
		t.Fatalf("expected max_findings 5, got %d", cfg.Notifications.Slack.MaxFindings)
	}
}
