package handlers

import (
	"encoding/json"
	"net/http"
)

// ReadinessChecker reports whether the system is ready to serve traffic.
type ReadinessChecker interface {
	Ready() bool
}

// HealthHandler serves liveness and readiness probes.
type HealthHandler struct {
	checker ReadinessChecker
}

// NewHealthHandler creates a HealthHandler with the given readiness checker.
func NewHealthHandler(checker ReadinessChecker) *HealthHandler {
	return &HealthHandler{checker: checker}
}

// Live returns 200 if the process is running.
func (h *HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

// Ready returns 200 if storage is open and the system is ready.
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	if h.checker != nil && h.checker.Ready() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
