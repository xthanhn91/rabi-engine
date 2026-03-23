package roles

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/nextlevelbuilder/goclaw/internal/config"
)

// GenerateChildConfig creates a temporary config file for a child role engine.
// It deep-copies the parent config, overrides role-specific fields (port, token,
// workspace), and disables features that only the parent should run
// (channels, cron, tailscale). Returns the temp file path and a cleanup function.
func GenerateChildConfig(parent *config.Config, roleName string, spec config.RoleSpec, port int) (string, func(), error) {
	// Deep-copy via JSON round-trip to avoid shared references
	data, err := json.Marshal(parent)
	if err != nil {
		return "", nil, fmt.Errorf("marshal parent config: %w", err)
	}

	var child config.Config
	if err := json.Unmarshal(data, &child); err != nil {
		return "", nil, fmt.Errorf("unmarshal child config: %w", err)
	}

	// Override gateway settings for child
	child.Gateway.Port = port
	child.Gateway.Token = deriveChildToken(parent.Gateway.Token, roleName)

	// Override workspace if role specifies one
	if spec.Workspace != "" {
		child.Agents.Defaults.Workspace = spec.Workspace
	}

	// Override LLM provider/model if role specifies them
	if spec.Provider != "" {
		child.Agents.Defaults.Provider = spec.Provider
	}
	if spec.Model != "" {
		child.Agents.Defaults.Model = spec.Model
	}

	// Disable features that only the parent engine should manage
	child.Channels = config.ChannelsConfig{}
	child.Cron = config.CronConfig{}
	child.Tailscale = config.TailscaleConfig{}
	child.Roles = config.RolesConfig{} // prevent recursive role spawning
	child.Bindings = nil

	// Write to temp file
	f, err := os.CreateTemp("", "goclaw-role-"+roleName+"-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("create temp config: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&child); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("write child config: %w", err)
	}
	f.Close()

	cleanup := func() { os.Remove(f.Name()) }
	return f.Name(), cleanup, nil
}

// deriveChildToken creates a deterministic, cryptographically-derived token for a child role.
// Uses HMAC-SHA256 so: (a) child can't derive sibling tokens, (b) parent token not recoverable,
// (c) parent can still compute the same token for authentication.
func deriveChildToken(parentToken, roleName string) string {
	if parentToken == "" {
		return roleName
	}
	mac := hmac.New(sha256.New, []byte(parentToken))
	mac.Write([]byte("goclaw-role:" + roleName))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}
