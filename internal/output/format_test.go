package output

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/driftctl/driftctl/internal/model"
)

func TestWriteJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	report := &model.DriftReport{ScanID: "scan-123", Status: model.ScanStatusCompleted}
	if err := WriteJSONFile(path, report); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected permissions 0600, got %04o", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded model.DriftReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ScanID != report.ScanID {
		t.Fatalf("expected scan ID %q, got %q", report.ScanID, decoded.ScanID)
	}
}

func TestTableIncludesErrors(t *testing.T) {
	report := &model.DriftReport{
		ScanID: "scan-123",
		Status: model.ScanStatusCompleted,
		Errors: []string{"cloud lookup failed"},
	}
	var out bytes.Buffer
	if err := Format(&out, report, "table"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !bytes.Contains([]byte(got), []byte("cloud lookup failed")) {
		t.Fatalf("expected table output to include report errors, got:\n%s", got)
	}
}
