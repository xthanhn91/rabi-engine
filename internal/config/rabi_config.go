// Package config — rabi_config.go extends the GoClaw Config with Rabi-specific sections.
// Upstream-safe: all Rabi types live in this file, Config struct only adds 2 fields.
package config

// RolesConfig configures the multi-role engine (Rabi spawns child processes per role).
type RolesConfig struct {
	Enabled  bool                `json:"enabled"`
	SkillDir string              `json:"skill_dir,omitempty"`
	List     map[string]RoleSpec `json:"list,omitempty"`
}

// RoleSpec defines a single child role engine.
type RoleSpec struct {
	Enabled     bool   `json:"enabled"`
	Brain       string `json:"brain,omitempty"`        // NeuralMemory brain for isolation
	Workspace   string `json:"workspace,omitempty"`    // override workspace
	Provider    string `json:"provider,omitempty"`     // LLM provider override
	Model       string `json:"model,omitempty"`        // LLM model override
	AutoStart   bool   `json:"auto_start,omitempty"`   // start on gateway boot
	MaxRestarts int    `json:"max_restarts,omitempty"` // 0 = default (3)
}

// FacebookConfig configures the optional Facebook browser automation sidecar.
type FacebookConfig struct {
	Enabled    bool   `json:"enabled"`
	SidecarURL string `json:"sidecar_url"`  // default: "http://localhost:7788"
	AccountID  string `json:"account_id"`   // profile ID for sidecar
	Email      string `json:"email"`        // Facebook login email
	Password   string `json:"password"`     // Facebook login password
}

// LoopHookDef defines a hook in the agent loop lifecycle.
// Exit code semantics: 0=inject stdout, 1=skip, 2=block.
type LoopHookDef struct {
	Event   string `json:"event"`            // pre_run, post_run, pre_tool, post_tool, pre_spawn, post_spawn
	Tool    string `json:"tool,omitempty"`   // filter: only fire for this tool
	Command string `json:"command"`          // shell command to execute
	Inject  string `json:"inject"`           // system_prompt, tool_result, or block
	Timeout int    `json:"timeout,omitempty"` // seconds (default: 5, max: 30)
}
