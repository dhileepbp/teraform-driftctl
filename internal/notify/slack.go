package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/driftctl/driftctl/internal/model"
)

const slackAPIURL = "https://slack.com/api/chat.postMessage"

// SlackConfig configures Slack delivery. BotTokenEnv is recommended when
// posting to multiple channels. WebhookEnv supports a Slack incoming webhook.
type SlackConfig struct {
	Enabled     bool     `json:"enabled" yaml:"enabled"`
	BotTokenEnv string   `json:"bot_token_env,omitempty" yaml:"bot_token_env,omitempty"`
	WebhookEnv  string   `json:"webhook_env,omitempty" yaml:"webhook_env,omitempty"`
	Channels    []string `json:"channels,omitempty" yaml:"channels,omitempty"`
	NotifyOn    []string `json:"notify_on,omitempty" yaml:"notify_on,omitempty"`
	MaxFindings int      `json:"max_findings,omitempty" yaml:"max_findings,omitempty"`
	APIURL      string   `json:"-" yaml:"-"`
	WebhookURL  string   `json:"-" yaml:"-"`
	BotToken    string   `json:"-" yaml:"-"`
}

// Slack sends compact drift reports using chat.postMessage or an incoming webhook.
type Slack struct {
	cfg    SlackConfig
	client *http.Client
}

func NewSlack(cfg SlackConfig) (*Slack, error) {
	return newSlack(cfg, &http.Client{Timeout: 10 * time.Second})
}

func newSlack(cfg SlackConfig, client *http.Client) (*Slack, error) {
	if cfg.BotToken == "" && cfg.BotTokenEnv != "" {
		cfg.BotToken = os.Getenv(cfg.BotTokenEnv)
	}
	if cfg.WebhookURL == "" && cfg.WebhookEnv != "" {
		cfg.WebhookURL = os.Getenv(cfg.WebhookEnv)
	}
	if cfg.APIURL == "" {
		cfg.APIURL = slackAPIURL
	}
	if cfg.MaxFindings <= 0 {
		cfg.MaxFindings = 10
	}
	if len(cfg.NotifyOn) == 0 {
		cfg.NotifyOn = []string{"drift", "error"}
	}
	if cfg.BotToken == "" && cfg.WebhookURL == "" {
		return nil, fmt.Errorf("Slack requires a bot token or webhook URL")
	}
	if cfg.BotToken != "" && len(cfg.Channels) == 0 {
		return nil, fmt.Errorf("Slack bot token delivery requires at least one channel")
	}
	return &Slack{cfg: cfg, client: client}, nil
}

func (s *Slack) Notify(ctx context.Context, report *model.DriftReport) error {
	if report == nil || !s.shouldNotify(report) {
		return nil
	}
	text := formatSlackMessage(report, s.cfg.MaxFindings)
	if s.cfg.BotToken != "" {
		for _, channel := range s.cfg.Channels {
			if err := s.postBotMessage(ctx, channel, text); err != nil {
				return err
			}
		}
		return nil
	}

	channels := s.cfg.Channels
	if len(channels) == 0 {
		channels = []string{""}
	}
	for _, channel := range channels {
		if err := s.postWebhook(ctx, channel, text); err != nil {
			return err
		}
	}
	return nil
}

func (s *Slack) shouldNotify(report *model.DriftReport) bool {
	for _, event := range s.cfg.NotifyOn {
		switch strings.ToLower(strings.TrimSpace(event)) {
		case "always", "all":
			return true
		case "drift":
			if report.HasDrift() {
				return true
			}
		case "error", "failed", "failure":
			if report.Status == model.ScanStatusFailed || len(report.Errors) > 0 {
				return true
			}
		case "clean", "no_drift":
			if report.Status == model.ScanStatusCompleted && !report.HasDrift() && len(report.Errors) == 0 {
				return true
			}
		}
	}
	return false
}

func (s *Slack) postBotMessage(ctx context.Context, channel, text string) error {
	payload := map[string]string{"channel": channel, "text": text}
	respBody, status, err := s.postJSON(ctx, s.cfg.APIURL, payload, "Bearer "+s.cfg.BotToken)
	if err != nil {
		return fmt.Errorf("post Slack message to %s: %w", channel, err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("post Slack message to %s: HTTP %d: %s", channel, status, strings.TrimSpace(string(respBody)))
	}
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("decode Slack response for %s: %w", channel, err)
	}
	if !result.OK {
		return fmt.Errorf("post Slack message to %s: %s", channel, result.Error)
	}
	return nil
}

func (s *Slack) postWebhook(ctx context.Context, channel, text string) error {
	payload := map[string]string{"text": text}
	if channel != "" {
		payload["channel"] = channel
	}
	respBody, status, err := s.postJSON(ctx, s.cfg.WebhookURL, payload, "")
	if err != nil {
		return fmt.Errorf("post Slack webhook: %w", safeTransportError(err))
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("post Slack webhook: HTTP %d: %s", status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// url.Error includes the requested URL. Slack webhook URLs are credentials, so
// unwrap transport errors before they are logged or stored with a scan.
func safeTransportError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

func (s *Slack) postJSON(ctx context.Context, url string, payload any, authorization string) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return respBody, resp.StatusCode, err
}

func formatSlackMessage(report *model.DriftReport, maxFindings int) string {
	status := "✅ No drift"
	if report.Status == model.ScanStatusFailed || len(report.Errors) > 0 {
		status = "❌ Scan error"
	} else if report.HasDrift() {
		status = "⚠️ Drift detected"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s in *%s*\n", status, report.Workspace)
	fmt.Fprintf(&b, "Scan `%s` · resources %d · findings %d", report.ScanID, report.Summary.TotalResources, report.Summary.TotalFindings)
	if report.HasDrift() {
		fmt.Fprintf(&b, "\nMissing %d · extra %d · attributes %d · tags %d",
			report.Summary.MissingInCloud, report.Summary.ExtraInCloud,
			report.Summary.AttributeChanges, report.Summary.TagChanges)
	}
	for i, finding := range report.Findings {
		if i >= maxFindings {
			fmt.Fprintf(&b, "\n…and %d more", len(report.Findings)-maxFindings)
			break
		}
		fmt.Fprintf(&b, "\n• `%s` %s (%s)", finding.Kind, finding.ResourceName, finding.ResourceType)
		if finding.Field != "" {
			fmt.Fprintf(&b, " — %s", finding.Field)
		}
	}
	for _, scanErr := range report.Errors {
		fmt.Fprintf(&b, "\n• error: %s", scanErr)
	}
	return b.String()
}
