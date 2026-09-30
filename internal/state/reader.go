package state

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/driftctl/driftctl/internal/model"
)

// Reader loads raw Terraform state bytes from various backends.
type Reader interface {
	Read(ctx context.Context, cfg model.StateConfig) ([]byte, error)
}

// DefaultReader supports local files and HTTP(S) backends.
type DefaultReader struct{}

func NewReader() *DefaultReader {
	return &DefaultReader{}
}

func (r *DefaultReader) Read(ctx context.Context, cfg model.StateConfig) ([]byte, error) {
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == "" {
		backend = "local"
	}

	switch backend {
	case "local", "file":
		return readLocal(cfg.Path)
	case "http", "https":
		return readHTTP(ctx, cfg.Path)
	default:
		return nil, fmt.Errorf("unsupported state backend: %s", cfg.Backend)
	}
}

func readLocal(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("state path is required for local backend")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}
	return data, nil
}

func readHTTP(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("state URL is required for http backend")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch state from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch state from %s: status %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}
