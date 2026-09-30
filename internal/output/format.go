package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/driftctl/driftctl/internal/model"
)

// Format renders a drift report.
func Format(w io.Writer, report *model.DriftReport, format string) error {
	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case "table":
		return writeTable(w, report)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

func writeTable(w io.Writer, report *model.DriftReport) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Scan ID:\t%s\n", report.ScanID)
	fmt.Fprintf(tw, "Workspace:\t%s\n", report.Workspace)
	fmt.Fprintf(tw, "Status:\t%s\n", report.Status)
	fmt.Fprintf(tw, "Started:\t%s\n", report.StartedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(tw, "Completed:\t%s\n", report.CompletedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "SUMMARY")
	fmt.Fprintf(tw, "Total Resources:\t%d\n", report.Summary.TotalResources)
	fmt.Fprintf(tw, "Missing in Cloud:\t%d\n", report.Summary.MissingInCloud)
	fmt.Fprintf(tw, "Attribute Changes:\t%d\n", report.Summary.AttributeChanges)
	fmt.Fprintf(tw, "Tag Changes:\t%d\n", report.Summary.TagChanges)
	fmt.Fprintf(tw, "Total Findings:\t%d\n", report.Summary.TotalFindings)
	fmt.Fprintln(tw, "")

	if len(report.Findings) == 0 {
		if report.Status == model.ScanStatusFailed {
			fmt.Fprintln(tw, "No drift findings produced.")
		} else {
			fmt.Fprintln(tw, "No drift detected.")
		}
	} else {
		fmt.Fprintln(tw, "FINDINGS")
		fmt.Fprintln(tw, "KIND\tSEVERITY\tRESOURCE\tFIELD\tEXPECTED\tACTUAL")
		for _, f := range report.Findings {
			fmt.Fprintf(tw, "%s\t%s\t%s (%s)\t%s\t%v\t%v\n",
				f.Kind, f.Severity, f.ResourceName, f.ResourceType, f.Field, f.Expected, f.Actual)
		}
	}

	if len(report.Errors) > 0 {
		fmt.Fprintln(tw, "")
		fmt.Fprintln(tw, "ERRORS")
		for _, reportErr := range report.Errors {
			fmt.Fprintf(tw, "- %s\n", reportErr)
		}
	}
	return tw.Flush()
}

// HasDrift returns true if the report contains actionable drift.
func HasDrift(report *model.DriftReport) bool {
	return report.HasDrift()
}

// WriteJSONFile writes a report atomically with owner-only permissions.
func WriteJSONFile(path string, report *model.DriftReport) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("JSON output path is required")
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tfdriftctl-*.json")
	if err != nil {
		return fmt.Errorf("create JSON output: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		tmp.Close()
		return fmt.Errorf("encode JSON output: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace JSON output: %w", err)
	}
	return nil
}
