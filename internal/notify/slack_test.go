package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/driftctl/driftctl/internal/model"
)

func TestSlackBotPostsToEveryChannel(t *testing.T) {
	var mu sync.Mutex
	var channels []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("unexpected authorization header %q", got)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		channels = append(channels, payload["channel"])
		mu.Unlock()
		return jsonResponse(http.StatusOK, `{"ok":true}`), nil
	})}

	slack, err := newSlack(SlackConfig{
		BotToken: "test-token",
		Channels: []string{"C123", "C456"},
		NotifyOn: []string{"drift"},
		APIURL:   "https://slack.test/api",
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	report := driftReport()
	if err := slack.Notify(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(channels, []string{"C123", "C456"}) {
		t.Fatalf("unexpected channels: %v", channels)
	}
}

func TestSlackSkipsCleanReportByDefault(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return jsonResponse(http.StatusOK, `{"ok":true}`), nil
	})}

	slack, err := newSlack(SlackConfig{
		BotToken: "test-token",
		Channels: []string{"C123"},
		APIURL:   "https://slack.test/api",
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	report := driftReport()
	report.Summary.TotalFindings = 0
	report.Findings = nil
	if err := slack.Notify(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("expected no Slack request, got %d", requests)
	}
}

func TestSlackWebhook(t *testing.T) {
	var payload map[string]string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return jsonResponse(http.StatusOK, "ok"), nil
	})}

	slack, err := newSlack(SlackConfig{
		WebhookURL: "https://hooks.slack.test/services/example",
		NotifyOn:   []string{"always"},
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := slack.Notify(context.Background(), driftReport()); err != nil {
		t.Fatal(err)
	}
	if payload["text"] == "" {
		t.Fatal("expected a Slack message")
	}
	if _, ok := payload["channel"]; ok {
		t.Fatal("webhook payload should use its configured channel when no override is provided")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func driftReport() *model.DriftReport {
	return &model.DriftReport{
		ScanID:    "scan-123",
		Workspace: "prod",
		Status:    model.ScanStatusCompleted,
		Summary: model.DriftSummary{
			TotalResources: 1,
			MissingInCloud: 1,
			TotalFindings:  1,
		},
		Findings: []model.DriftFinding{{
			Kind:         model.DriftMissingInCloud,
			ResourceType: "azurerm_linux_virtual_machine",
			ResourceName: "web",
		}},
	}
}
