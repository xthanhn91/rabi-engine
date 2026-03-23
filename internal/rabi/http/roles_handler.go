package rabihttp

import (
	"encoding/json"
	"net/http"

	"github.com/nextlevelbuilder/goclaw/internal/rabi/roles"
)

// RolesHandler exposes HTTP endpoints for managing child role engine processes.
// Follows the same pattern as GoClaw's AgentsHandler: struct + RegisterRoutes + authMiddleware.
type RolesHandler struct {
	manager *roles.RoleManager
	token   string
}

// NewRolesHandler creates a handler for role management endpoints.
func NewRolesHandler(manager *roles.RoleManager, token string) *RolesHandler {
	return &RolesHandler{manager: manager, token: token}
}

// RegisterRoutes registers all role management routes on the given mux.
func (h *RolesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/roles", h.authMiddleware(h.handleList))
	mux.HandleFunc("POST /v1/roles/{role}/spawn", h.authMiddleware(h.handleSpawn))
	mux.HandleFunc("DELETE /v1/roles/{role}", h.authMiddleware(h.handleStop))
	mux.HandleFunc("GET /v1/roles/{role}/status", h.authMiddleware(h.handleStatus))
	mux.HandleFunc("POST /v1/roles/{role}/dispatch", h.authMiddleware(h.handleDispatch))
}

func (h *RolesHandler) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenMatch(extractBearerToken(r), h.token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

// handleList returns all configured roles and their current status.
func (h *RolesHandler) handleList(w http.ResponseWriter, r *http.Request) {
	infos := h.manager.Status()
	writeJSON(w, http.StatusOK, map[string]any{"roles": infos})
}

// handleSpawn starts a specific role engine process.
func (h *RolesHandler) handleSpawn(w http.ResponseWriter, r *http.Request) {
	roleName := r.PathValue("role")
	if roleName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role name required"})
		return
	}

	if err := h.manager.Spawn(r.Context(), roleName); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	info, _ := h.manager.RoleStatus(roleName)
	writeJSON(w, http.StatusOK, info)
}

// handleStop stops a running role engine process.
func (h *RolesHandler) handleStop(w http.ResponseWriter, r *http.Request) {
	roleName := r.PathValue("role")
	if roleName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role name required"})
		return
	}

	if err := h.manager.StopRole(roleName); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

// handleStatus returns the status of a single role.
func (h *RolesHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	roleName := r.PathValue("role")
	if roleName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role name required"})
		return
	}

	info, found := h.manager.RoleStatus(roleName)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "role not found"})
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// dispatchRequest is the JSON body for POST /v1/roles/{role}/dispatch.
type dispatchRequest struct {
	Task string `json:"task"`
}

// handleDispatch sends a task to a running role engine.
func (h *RolesHandler) handleDispatch(w http.ResponseWriter, r *http.Request) {
	roleName := r.PathValue("role")
	if roleName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role name required"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	var req dispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Task == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task field is required"})
		return
	}

	result, err := h.manager.Dispatch(r.Context(), roleName, req.Task)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Return the raw response from the child engine
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(result))
}
