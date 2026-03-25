package license

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// GracePeriod is how long the engine runs without contacting the
	// license server. After this period, the engine refuses to start.
	GracePeriod = 7 * 24 * time.Hour // 7 days

	// PhoneHomeInterval is how often the engine validates with the server.
	PhoneHomeInterval = 24 * time.Hour

	// DefaultServerURL is the production license server endpoint.
	DefaultServerURL = "https://license.goclaw.ai"
)

// ValidateRequest is sent to the license server for validation.
type ValidateRequest struct {
	Key       string `json:"key"`
	MachineID string `json:"machine_id"`
	Hostname  string `json:"hostname,omitempty"`
}

// ValidateResponse is returned by the license server.
type ValidateResponse struct {
	Valid      bool              `json:"valid"`
	Plan       string            `json:"plan"`
	MaxAgents  int               `json:"max_agents"`
	MaxTenants int               `json:"max_tenants"`
	Features   map[string]bool   `json:"features"`
	ExpiresAt  *string           `json:"expires_at"`
	Message    string            `json:"message,omitempty"`
}

// LocalState persists the last successful validation to disk so
// the engine can start during temporary server outages (grace period).
// KeyHash stores SHA-256 of the key (never plaintext) for verification.
type LocalState struct {
	KeyHash       string    `json:"key_hash"`
	Plan          string    `json:"plan"`
	MaxAgents     int       `json:"max_agents"`
	MaxTenants    int       `json:"max_tenants"`
	Features      map[string]bool `json:"features"`
	LastValidated time.Time `json:"last_validated"`
}

// hashKey returns hex-encoded SHA-256 of a license key.
func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// Validator handles license key validation and periodic phone-home.
type Validator struct {
	serverURL string
	stateFile string
	client    *http.Client
}

// NewValidator creates a validator.
// serverURL: license server base URL (empty = default production).
// stateDir: directory to store license.json (typically same as config dir).
func NewValidator(serverURL, stateDir string) *Validator {
	if serverURL == "" {
		serverURL = DefaultServerURL
	}
	return &Validator{
		serverURL: serverURL,
		stateFile: filepath.Join(stateDir, "license.json"),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Validate checks the license key against the server. On success,
// it persists the validation state to disk. On server failure, it
// checks the local state for grace period eligibility.
func (v *Validator) Validate(key string) (*ValidateResponse, error) {
	if key == "" {
		return nil, fmt.Errorf("no license key provided — run: ./goclaw activate --key YOUR-KEY")
	}

	hostname, _ := os.Hostname()
	reqBody := ValidateRequest{
		Key:       key,
		MachineID: MachineID(),
		Hostname:  hostname,
	}

	resp, err := v.callServer(reqBody)
	if err != nil {
		// Server unreachable — check grace period from local state
		slog.Warn("license server unreachable, checking grace period", "error", err)
		return v.checkGracePeriod(key)
	}

	if !resp.Valid {
		return nil, fmt.Errorf("license invalid: %s", resp.Message)
	}

	// Persist successful validation (key stored as hash, never plaintext)
	state := LocalState{
		KeyHash:       hashKey(key),
		Plan:          resp.Plan,
		MaxAgents:     resp.MaxAgents,
		MaxTenants:    resp.MaxTenants,
		Features:      resp.Features,
		LastValidated: time.Now(),
	}
	if err := v.saveState(state); err != nil {
		slog.Warn("failed to save license state", "error", err)
	}

	return resp, nil
}

// callServer sends a validation request to the license server.
func (v *Validator) callServer(req ValidateRequest) (*ValidateResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpResp, err := v.client.Post(
		v.serverURL+"/api/licenses/validate",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Check status code before attempting to decode — non-200 responses
	// may not be valid JSON (e.g. gateway errors, CF error pages).
	if httpResp.StatusCode != http.StatusOK {
		var resp ValidateResponse
		// Best-effort decode for error message; ignore decode failures.
		_ = json.NewDecoder(httpResp.Body).Decode(&resp)
		return nil, fmt.Errorf("server returned %d: %s", httpResp.StatusCode, resp.Message)
	}

	var resp ValidateResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &resp, nil
}

// checkGracePeriod reads the local state and allows startup if the
// last validation was within the grace period.
func (v *Validator) checkGracePeriod(key string) (*ValidateResponse, error) {
	state, err := v.loadState()
	if err != nil {
		return nil, fmt.Errorf("no local license state and server unreachable — cannot validate")
	}

	if state.KeyHash != hashKey(key) {
		return nil, fmt.Errorf("license key changed since last validation — server required")
	}

	elapsed := time.Since(state.LastValidated)
	if elapsed > GracePeriod {
		return nil, fmt.Errorf("license server unreachable for %s (grace period: %s) — please check connectivity",
			elapsed.Round(time.Hour), GracePeriod)
	}

	slog.Info("using cached license validation",
		"last_validated", state.LastValidated.Format(time.RFC3339),
		"grace_remaining", (GracePeriod - elapsed).Round(time.Hour),
	)

	return &ValidateResponse{
		Valid:      true,
		Plan:       state.Plan,
		MaxAgents:  state.MaxAgents,
		MaxTenants: state.MaxTenants,
		Features:   state.Features,
	}, nil
}

func (v *Validator) saveState(state LocalState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(v.stateFile, data, 0600)
}

func (v *Validator) loadState() (*LocalState, error) {
	data, err := os.ReadFile(v.stateFile)
	if err != nil {
		return nil, err
	}
	var state LocalState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}
