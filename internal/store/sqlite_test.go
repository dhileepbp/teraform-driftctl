package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/driftctl/driftctl/internal/model"
)

func TestWorkspaceSubscriptionsRoundTrip(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	workspace := &model.Workspace{
		Name:          "azure-prod",
		Provider:      "azure",
		Subscriptions: []string{"sub-one", "sub-two"},
	}
	if err := store.SaveWorkspace(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetWorkspaceByName(context.Background(), workspace.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Subscriptions, workspace.Subscriptions) {
		t.Fatalf("expected subscriptions %v, got %v", workspace.Subscriptions, loaded.Subscriptions)
	}
}

func TestOpenSQLiteMigratesWorkspaceSubscriptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  provider TEXT NOT NULL,
  state_json TEXT NOT NULL,
  regions_json TEXT NOT NULL,
  compare_json TEXT NOT NULL,
  created_at TEXT NOT NULL
)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	workspace := &model.Workspace{Name: "migrated", Provider: "azure", Subscriptions: []string{"sub-one"}}
	if err := store.SaveWorkspace(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetWorkspaceByName(context.Background(), "migrated")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Subscriptions, workspace.Subscriptions) {
		t.Fatalf("expected subscriptions %v, got %v", workspace.Subscriptions, loaded.Subscriptions)
	}
}
