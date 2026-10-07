package app

// The reload admin endpoint (ADR-0015 decisions 13–17).
//
// Concurrency: every reload, from SIGHUP or from POST /admin/reload, runs
// under App.reloadMu, so the registry apply has one writer at a time. The last
// outcome is published through App.lastReload, one atomic pointer, and read
// lock-free by GET /admin/reload/last. The run context arrives through
// App.runCtx, stored by Run before any listener starts serving.
//
// This is a demo and test affordance on the unpublished metrics listener, not
// a production control plane: no authentication, no persistence, no audit
// beyond the reload log line (ADR-0005; ADR-0013 amendment of 2026-10-07).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/DMJain/l7LoadBalancer/internal/config"
	"github.com/DMJain/l7LoadBalancer/internal/logger"
)

// Reload outcomes as served by GET /admin/reload/last.
const (
	ReloadResultNone     = "none"
	ReloadResultSuccess  = "success"
	ReloadResultRejected = "rejected"
)

// adminReloadMaxBody caps a POST /admin/reload body. A config is a few
// kilobytes; 1 MiB leaves room for a large backend list and bounds what one
// request can make the process buffer.
const adminReloadMaxBody = 1 << 20

// adminReloadSource names a request body in config.Parse errors.
const adminReloadSource = "admin request body"

// ReloadStatus is the outcome of one reload attempt. Time is when the attempt
// finished; Error is empty on success.
type ReloadStatus struct {
	Result string
	Time   time.Time
	Error  string
}

// reloadStatusJSON is ReloadStatus on the wire: Time as RFC 3339, and neither
// time nor error present for "none".
type reloadStatusJSON struct {
	Result string `json:"result"`
	Time   string `json:"time,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (s ReloadStatus) wire() reloadStatusJSON {
	w := reloadStatusJSON{Result: s.Result, Error: s.Error}
	if !s.Time.IsZero() {
		w.Time = s.Time.UTC().Format(time.RFC3339)
	}
	return w
}

// LastReload returns the outcome of the most recent reload attempt from either
// trigger, or a "none" status before the first one. It is safe to call
// concurrently with a reload.
func (a *App) LastReload() ReloadStatus {
	if s := a.lastReload.Load(); s != nil {
		return *s
	}
	return ReloadStatus{Result: ReloadResultNone}
}

// recordReload publishes the outcome of an attempt; the caller holds reloadMu.
func (a *App) recordReload(err error) ReloadStatus {
	s := ReloadStatus{Result: ReloadResultSuccess, Time: time.Now()}
	if err != nil {
		s.Result = ReloadResultRejected
		s.Error = err.Error()
	}
	a.lastReload.Store(&s)
	return s
}

// ReloadFrom is the one reload operation both triggers share (ADR-0015
// decision 14): it obtains a config from load (main reads the file, the admin
// endpoint parses the request body), validates it, and applies it with Reload's
// rules, all under the reload mutex, and records the outcome. A load failure is
// logged as config_reload_failed/parse_error and a validation failure as
// config_reload_failed/validation_error, exactly as the SIGHUP loop logged them
// before this operation existed; Reload's own rejections log themselves. In
// every rejection the previously loaded config keeps serving.
func (a *App) ReloadFrom(ctx context.Context, load func() (*config.Config, error)) (ReloadStatus, error) {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()

	err := a.loadValidateApply(ctx, load)
	return a.recordReload(err), err
}

func (a *App) loadValidateApply(ctx context.Context, load func() (*config.Config, error)) error {
	cfg, err := load()
	if err != nil {
		a.log.Warn("config reload failed",
			"event", logger.EventConfigReloadFailed,
			"reason", logger.ReasonParseError,
			"err", err,
		)
		return err
	}
	if err := cfg.Validate(); err != nil {
		a.log.Warn("config reload failed",
			"event", logger.EventConfigReloadFailed,
			"reason", logger.ReasonValidationError,
			"err", err,
		)
		return err
	}
	return a.reloadLocked(ctx, cfg)
}

// adminHandler serves the two reload admin routes. Method-qualified patterns
// make the mux answer 405 for any other method.
func (a *App) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/reload", a.handleAdminReload)
	mux.HandleFunc("GET /admin/reload/last", a.handleAdminReloadLast)
	return mux
}

// handleAdminReload applies the YAML config in the request body: 200 with the
// recorded status on success, 422 with the error on any rejection, 413 for a
// body over adminReloadMaxBody, 503 before Run has started. A 413 or 503 is not
// a reload attempt and is not recorded.
func (a *App) handleAdminReload(w http.ResponseWriter, r *http.Request) {
	runCtx := a.runCtx.Load()
	if runCtx == nil {
		http.Error(w, "load balancer not running", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, adminReloadMaxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "config body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	status, err := a.ReloadFrom(*runCtx, func() (*config.Config, error) {
		return config.Parse(bytes.NewReader(body), adminReloadSource)
	})
	code := http.StatusOK
	if err != nil {
		code = http.StatusUnprocessableEntity
	}
	writeJSON(w, code, status.wire())
}

// handleAdminReloadLast serves the last attempt's outcome.
func (a *App) handleAdminReloadLast(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.LastReload().wire())
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
