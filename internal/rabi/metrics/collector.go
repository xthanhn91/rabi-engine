package metrics

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/rabi/roles"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// MetricsCollector aggregates runtime data for the Jarvis dashboard.
// Sessions dependency removed — GoClaw uses PostgreSQL sessions, not file-based Manager.
// TotalTokens and ActiveSessions are set to 0 until wired to PG store.
type MetricsCollector struct {
	startedAt time.Time
	roleMgr   *roles.RoleManager
	toolsReg  *tools.Registry
	cfg       *config.Config
	version   string
	workspace string

	// Cached monitoring count (registry is static at runtime)
	monitorOnce  sync.Once
	monitorCount int
}

// NewMetricsCollector creates a collector for Jarvis dashboard metrics.
func NewMetricsCollector(
	startedAt time.Time,
	roleMgr *roles.RoleManager,
	toolsReg *tools.Registry,
	cfg *config.Config,
	version string,
	workspace string,
) *MetricsCollector {
	return &MetricsCollector{
		startedAt: startedAt,
		roleMgr:   roleMgr,
		toolsReg:  toolsReg,
		cfg:       cfg,
		version:   version,
		workspace: workspace,
	}
}

// HealthMetrics represents vital signs.
type HealthMetrics struct {
	UptimeSeconds         int64  `json:"uptime_seconds"`
	Version               string `json:"version"`
	Model                 string `json:"model"`
	Provider              string `json:"provider"`
	ContextWindow         int    `json:"context_window"`
	TotalInputTokens      int64  `json:"total_input_tokens"`
	TotalOutputTokens     int64  `json:"total_output_tokens"`
	ActiveSessions        int    `json:"active_sessions"`
	ToolsRegistered       int    `json:"tools_registered"`
	MonitoringActiveCount int    `json:"monitoring_active_count"`
	Timestamp             string `json:"timestamp"`
}

// CognitiveMetrics represents NeuralMemory cognitive state (cached).
type CognitiveMetrics struct {
	Available bool   `json:"available"`
	Brain     string `json:"brain"`
	CachedAt  string `json:"cached_at"`
}

// OrgMetrics represents the organizational tree.
type OrgMetrics struct {
	Entity       string           `json:"entity"`
	Roles        []roles.RoleInfo `json:"roles"`
	RolesEnabled bool             `json:"roles_enabled"`
}

// OperationsMetrics represents tool and dispatch stats.
type OperationsMetrics struct {
	ToolsRegistered int      `json:"tools_registered"`
	ToolNames       []string `json:"tool_names"`
	ActiveSessions  int      `json:"active_sessions"`
}

// EvolutionMetrics represents self-improvement stats.
type EvolutionMetrics struct {
	AgentsMdModified string `json:"agents_md_modified"`
	AgentsMdLines    int    `json:"agents_md_lines"`
	SkillsCount      int    `json:"skills_count"`
}

func (c *MetricsCollector) CollectHealth() HealthMetrics {
	m := HealthMetrics{
		UptimeSeconds: int64(time.Since(c.startedAt).Seconds()),
		Version:       c.version,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		// TotalInputTokens, TotalOutputTokens, ActiveSessions: 0 until PG store wired
	}

	if c.cfg != nil {
		m.Model = c.cfg.Agents.Defaults.Model
		m.Provider = c.cfg.Agents.Defaults.Provider
		m.ContextWindow = c.cfg.Agents.Defaults.ContextWindow
	}

	if c.toolsReg != nil {
		m.ToolsRegistered = c.toolsReg.Count()
	}

	m.MonitoringActiveCount = c.countActiveMonitored()

	return m
}

func (c *MetricsCollector) CollectCognitive() CognitiveMetrics {
	// V1: report availability only. Full nmem integration in V2.
	return CognitiveMetrics{
		Available: false,
		Brain:     "rabi",
		CachedAt:  time.Now().UTC().Format(time.RFC3339),
	}
}

func (c *MetricsCollector) CollectOrg() OrgMetrics {
	m := OrgMetrics{Entity: "Rabi"}

	if c.roleMgr != nil {
		m.RolesEnabled = true
		m.Roles = c.roleMgr.Status()
	} else {
		m.RolesEnabled = false
		m.Roles = []roles.RoleInfo{}
	}

	return m
}

func (c *MetricsCollector) CollectOperations() OperationsMetrics {
	m := OperationsMetrics{
		ToolNames:      []string{}, // never nil — prevents JSON null
		ActiveSessions: 0,          // set to 0 until PG store wired
	}

	if c.toolsReg != nil {
		names := c.toolsReg.List()
		sort.Strings(names)
		m.ToolNames = names
		m.ToolsRegistered = len(names)
	}

	return m
}

func (c *MetricsCollector) CollectEvolution() EvolutionMetrics {
	m := EvolutionMetrics{}

	// Read AGENTS.md modification time and line count
	agentsPath := filepath.Join(c.workspace, "AGENTS.md")
	if info, err := os.Stat(agentsPath); err == nil {
		m.AgentsMdModified = info.ModTime().UTC().Format(time.RFC3339)
		m.AgentsMdLines = countLines(agentsPath)
	}

	// Count SKILL.md files in skills directories
	skillsDir := filepath.Join(c.workspace, "skills")
	m.SkillsCount = countSkills(skillsDir)

	return m
}

// countLines returns the number of lines in a file.
func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
	}
	return count
}

// countActiveMonitored reads project-registry.json once and caches the count.
func (c *MetricsCollector) countActiveMonitored() int {
	c.monitorOnce.Do(func() {
		registryPath := filepath.Join(c.workspace, "project-registry.json")
		data, err := os.ReadFile(registryPath)
		if err != nil {
			return
		}

		var registry struct {
			Projects []struct {
				Monitor string `json:"monitor"`
			} `json:"projects"`
		}
		if err := json.Unmarshal(data, &registry); err != nil {
			return
		}

		for _, p := range registry.Projects {
			if p.Monitor == "active" {
				c.monitorCount++
			}
		}
	})
	return c.monitorCount
}

// countSkills counts SKILL.md files under a directory (one level of subdirs).
func countSkills(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillFile := filepath.Join(dir, e.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); err == nil {
			count++
		}
	}
	return count
}
