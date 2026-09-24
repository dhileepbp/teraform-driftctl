package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/driftctl/driftctl/internal/config"
	"github.com/driftctl/driftctl/internal/model"
	"github.com/driftctl/driftctl/internal/output"
	"github.com/driftctl/driftctl/internal/scan"
	"github.com/driftctl/driftctl/internal/scheduler"
	"github.com/driftctl/driftctl/internal/store"
)

// Server exposes the drift detection REST API.
type Server struct {
	cfg       config.APIConfig
	store     store.Store
	scanner   *scan.Scanner
	scheduler *scheduler.Scheduler
	mux       *http.ServeMux
}

func NewServer(cfg config.APIConfig, st store.Store, scanner *scan.Scanner, sched *scheduler.Scheduler) *Server {
	s := &Server{
		cfg:       cfg,
		store:     st,
		scanner:   scanner,
		scheduler: sched,
		mux:       http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.authMiddleware(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/workspaces", s.handleListWorkspaces)
	s.mux.HandleFunc("POST /api/v1/workspaces", s.handleCreateWorkspace)
	s.mux.HandleFunc("GET /api/v1/workspaces/{id}", s.handleGetWorkspace)
	s.mux.HandleFunc("DELETE /api/v1/workspaces/{id}", s.handleDeleteWorkspace)
	s.mux.HandleFunc("POST /api/v1/workspaces/{id}/scans", s.handleTriggerScan)
	s.mux.HandleFunc("GET /api/v1/workspaces/{id}/scans", s.handleListWorkspaceScans)
	s.mux.HandleFunc("GET /api/v1/scans", s.handleListScans)
	s.mux.HandleFunc("GET /api/v1/scans/{id}", s.handleGetScan)
	s.mux.HandleFunc("GET /api/v1/scans/{id}/report", s.handleGetReport)
	s.mux.HandleFunc("PUT /api/v1/workspaces/{id}/schedules", s.handleUpsertSchedule)
	s.mux.HandleFunc("DELETE /api/v1/workspaces/{id}/schedules", s.handleDeleteSchedule)
	s.mux.HandleFunc("GET /", s.handleDashboard)
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey == "" || r.URL.Path == "/health" || r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if key != s.cfg.APIKey {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListWorkspaces(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var ws model.Workspace
	if err := json.NewDecoder(r.Body).Decode(&ws); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if ws.Name == "" || ws.Provider == "" {
		writeError(w, http.StatusBadRequest, "name and provider are required")
		return
	}
	ws.ID = uuid.New().String()
	if err := s.store.SaveWorkspace(r.Context(), &ws); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ws.Schedule != nil && ws.Schedule.Cron != "" {
		_ = s.store.SaveSchedule(r.Context(), ws.ID, ws.Schedule.Cron)
		_ = s.scheduler.Register(r.Context(), ws.ID, ws.Schedule.Cron)
	}
	writeJSON(w, http.StatusCreated, ws)
}

func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ws, err := s.store.GetWorkspace(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

func (s *Server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteWorkspace(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.scheduler.Unregister(id)
	_ = s.store.DeleteSchedule(r.Context(), id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTriggerScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ws, err := s.store.GetWorkspace(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	report, err := s.scanner.Run(r.Context(), scan.Options{
		WorkspaceID:   ws.ID,
		WorkspaceName: ws.Name,
		Provider:      ws.Provider,
		State:         ws.State,
		Regions:       ws.Regions,
		Subscriptions: ws.Subscriptions,
		Compare:       ws.Compare,
	})
	if err != nil && report == nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusAccepted
	}
	writeJSON(w, status, report)
}

func (s *Server) handleListWorkspaceScans(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	scans, err := s.store.ListScans(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scans)
}

func (s *Server) handleListScans(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	scans, err := s.store.ListScans(r.Context(), "", limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scans)
}

func (s *Server) handleGetScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	report, err := s.store.GetScan(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "scan not found")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleGetReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	report, err := s.store.GetScan(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "scan not found")
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	w.Header().Set("Content-Type", contentTypeForFormat(format))
	if err := output.Format(w, report, format); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *Server) handleUpsertSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Cron string `json:"cron"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Cron == "" {
		writeError(w, http.StatusBadRequest, "cron expression required")
		return
	}
	if _, err := s.store.GetWorkspace(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if err := s.store.SaveSchedule(r.Context(), id, body.Cron); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.scheduler.Register(r.Context(), id, body.Cron); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"workspace_id": id, "cron": body.Cron})
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteSchedule(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.scheduler.Unregister(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/index.html")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func contentTypeForFormat(format string) string {
	switch strings.ToLower(format) {
	case "table":
		return "text/plain; charset=utf-8"
	default:
		return "application/json"
	}
}

// Run starts the HTTP server.
func Run(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	fmt.Printf("drift-server listening on %s\n", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
