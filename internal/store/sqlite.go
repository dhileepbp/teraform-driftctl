package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"

	"github.com/driftctl/driftctl/internal/model"
)

// SQLiteStore implements Store with SQLite.
type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    provider TEXT NOT NULL,
    state_json TEXT NOT NULL,
    regions_json TEXT NOT NULL,
    subscriptions_json TEXT NOT NULL DEFAULT '[]',
    compare_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS scans (
    id TEXT PRIMARY KEY,
    workspace_id TEXT,
    workspace_name TEXT,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    summary_json TEXT,
    findings_json TEXT,
    errors_json TEXT
);

CREATE TABLE IF NOT EXISTS schedules (
    workspace_id TEXT PRIMARY KEY,
    cron TEXT NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	return s.ensureWorkspaceSubscriptionsColumn()
}

func (s *SQLiteStore) ensureWorkspaceSubscriptionsColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(workspaces)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "subscriptions_json" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE workspaces ADD COLUMN subscriptions_json TEXT NOT NULL DEFAULT '[]'`)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) SaveWorkspace(ctx context.Context, ws *model.Workspace) error {
	if ws.ID == "" {
		ws.ID = uuid.New().String()
	}
	stateJSON, _ := json.Marshal(ws.State)
	regionsJSON, _ := json.Marshal(ws.Regions)
	subscriptionsJSON, _ := json.Marshal(ws.Subscriptions)
	compareJSON, _ := json.Marshal(ws.Compare)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO workspaces (id, name, provider, state_json, regions_json, subscriptions_json, compare_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   provider=excluded.provider,
		   state_json=excluded.state_json,
		   regions_json=excluded.regions_json,
		   subscriptions_json=excluded.subscriptions_json,
		   compare_json=excluded.compare_json`,
		ws.ID, ws.Name, ws.Provider, string(stateJSON), string(regionsJSON), string(subscriptionsJSON), string(compareJSON), time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *SQLiteStore) GetWorkspace(ctx context.Context, id string) (*model.Workspace, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, provider, state_json, regions_json, subscriptions_json, compare_json FROM workspaces WHERE id = ?`, id)
	return scanWorkspace(row)
}

func (s *SQLiteStore) GetWorkspaceByName(ctx context.Context, name string) (*model.Workspace, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, provider, state_json, regions_json, subscriptions_json, compare_json FROM workspaces WHERE name = ?`, name)
	return scanWorkspace(row)
}

func scanWorkspace(row *sql.Row) (*model.Workspace, error) {
	var ws model.Workspace
	var stateJSON, regionsJSON, subscriptionsJSON, compareJSON string
	if err := row.Scan(&ws.ID, &ws.Name, &ws.Provider, &stateJSON, &regionsJSON, &subscriptionsJSON, &compareJSON); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(stateJSON), &ws.State)
	_ = json.Unmarshal([]byte(regionsJSON), &ws.Regions)
	_ = json.Unmarshal([]byte(subscriptionsJSON), &ws.Subscriptions)
	_ = json.Unmarshal([]byte(compareJSON), &ws.Compare)
	return &ws, nil
}

func (s *SQLiteStore) ListWorkspaces(ctx context.Context) ([]model.Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, provider, state_json, regions_json, subscriptions_json, compare_json FROM workspaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.Workspace
	for rows.Next() {
		var ws model.Workspace
		var stateJSON, regionsJSON, subscriptionsJSON, compareJSON string
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.Provider, &stateJSON, &regionsJSON, &subscriptionsJSON, &compareJSON); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(stateJSON), &ws.State)
		_ = json.Unmarshal([]byte(regionsJSON), &ws.Regions)
		_ = json.Unmarshal([]byte(subscriptionsJSON), &ws.Subscriptions)
		_ = json.Unmarshal([]byte(compareJSON), &ws.Compare)
		list = append(list, ws)
	}
	return list, nil
}

func (s *SQLiteStore) DeleteWorkspace(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveScan(ctx context.Context, report *model.DriftReport) error {
	summaryJSON, _ := json.Marshal(report.Summary)
	findingsJSON, _ := json.Marshal(report.Findings)
	errorsJSON, _ := json.Marshal(report.Errors)
	completed := ""
	if !report.CompletedAt.IsZero() {
		completed = report.CompletedAt.UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scans (id, workspace_id, workspace_name, status, started_at, completed_at, summary_json, findings_json, errors_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   status=excluded.status,
		   completed_at=excluded.completed_at,
		   summary_json=excluded.summary_json,
		   findings_json=excluded.findings_json,
		   errors_json=excluded.errors_json`,
		report.ScanID, report.WorkspaceID, report.Workspace, report.Status,
		report.StartedAt.UTC().Format(time.RFC3339), completed,
		string(summaryJSON), string(findingsJSON), string(errorsJSON),
	)
	return err
}

func (s *SQLiteStore) GetScan(ctx context.Context, id string) (*model.DriftReport, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, workspace_name, status, started_at, completed_at, summary_json, findings_json, errors_json FROM scans WHERE id = ?`, id)
	return scanReport(row)
}

func (s *SQLiteStore) ListScans(ctx context.Context, workspaceID string, limit int) ([]model.DriftReport, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if workspaceID != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, workspace_id, workspace_name, status, started_at, completed_at, summary_json, findings_json, errors_json
			 FROM scans WHERE workspace_id = ? ORDER BY started_at DESC LIMIT ?`, workspaceID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, workspace_id, workspace_name, status, started_at, completed_at, summary_json, findings_json, errors_json
			 FROM scans ORDER BY started_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.DriftReport
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *r)
	}
	return list, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanReport(row scanner) (*model.DriftReport, error) {
	var r model.DriftReport
	var started, completed sql.NullString
	var summaryJSON, findingsJSON, errorsJSON string
	if err := row.Scan(&r.ScanID, &r.WorkspaceID, &r.Workspace, &r.Status, &started, &completed, &summaryJSON, &findingsJSON, &errorsJSON); err != nil {
		return nil, err
	}
	if started.Valid {
		r.StartedAt, _ = time.Parse(time.RFC3339, started.String)
	}
	if completed.Valid && completed.String != "" {
		r.CompletedAt, _ = time.Parse(time.RFC3339, completed.String)
	}
	_ = json.Unmarshal([]byte(summaryJSON), &r.Summary)
	_ = json.Unmarshal([]byte(findingsJSON), &r.Findings)
	_ = json.Unmarshal([]byte(errorsJSON), &r.Errors)
	return &r, nil
}

func (s *SQLiteStore) SaveSchedule(ctx context.Context, workspaceID, cron string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO schedules (workspace_id, cron) VALUES (?, ?)
		 ON CONFLICT(workspace_id) DO UPDATE SET cron=excluded.cron`, workspaceID, cron)
	return err
}

func (s *SQLiteStore) GetSchedule(ctx context.Context, workspaceID string) (string, error) {
	var cron string
	err := s.db.QueryRowContext(ctx, `SELECT cron FROM schedules WHERE workspace_id = ?`, workspaceID).Scan(&cron)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no schedule for workspace %s", workspaceID)
	}
	return cron, err
}

func (s *SQLiteStore) ListSchedules(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id, cron FROM schedules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var wsID, cron string
		if err := rows.Scan(&wsID, &cron); err != nil {
			return nil, err
		}
		out[wsID] = cron
	}
	return out, nil
}

func (s *SQLiteStore) DeleteSchedule(ctx context.Context, workspaceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE workspace_id = ?`, workspaceID)
	return err
}
