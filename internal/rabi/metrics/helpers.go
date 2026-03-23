package metrics

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// writeJSON encodes data as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// extractBearerToken extracts a bearer token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

// tokenMatch performs a constant-time comparison against the expected token.
// Returns true if expected is empty (no auth configured) or tokens match.
func tokenMatch(provided, expected string) bool {
	if expected == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}
