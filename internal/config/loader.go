package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Manager handles loading and reloading configuration files.
type Manager struct {
	mu         sync.RWMutex
	globalPath string
	global     GlobalConfig
	rules      []*RuleConfig
}

// NewManager creates a new config Manager.
func NewManager(globalPath string) *Manager {
	return &Manager{
		globalPath: globalPath,
	}
}

// Load loads the global config and all rule configs.
// The in-memory state is only replaced after everything parsed
// successfully, so a failed reload keeps global and rules consistent.
func (m *Manager) Load() error {
	// Load global config
	g, err := loadGlobalConfig(m.globalPath)
	if err != nil {
		return fmt.Errorf("load global config: %w", err)
	}

	// Load rule configs
	rules, err := loadRuleConfigs(g.ConfigDir)
	if err != nil {
		return fmt.Errorf("load rule configs: %w", err)
	}

	m.mu.Lock()
	m.global = g
	m.rules = rules
	m.mu.Unlock()

	return nil
}

// Reload re-reads all configuration files from disk.
func (m *Manager) Reload() error {
	return m.Load()
}

// Global returns the current global config (read-only).
func (m *Manager) Global() GlobalConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.global
}

// Rules returns all loaded rule configs (read-only).
func (m *Manager) Rules() []*RuleConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*RuleConfig, len(m.rules))
	copy(result, m.rules)
	return result
}

// ValidateAll validates all loaded configs without applying them.
func (m *Manager) ValidateAll() []error {
	var errs []error

	// Validate global
	if err := m.global.Validate(); err != nil {
		errs = append(errs, err)
	}

	// Validate rules
	for _, r := range m.rules {
		if err := r.Validate(); err != nil {
			errs = append(errs, err)
		}
	}

	// Check for duplicate config names and routing file names
	seenNames := make(map[string]string)  // config name -> source file
	seenFiles := make(map[string]string)  // file name (routing key) -> source file
	for _, r := range m.rules {
		if prev, dup := seenNames[r.Name]; dup {
			errs = append(errs, fmt.Errorf("duplicate config name '%s' in '%s' and '%s'", r.Name, prev, r.FilePath))
		} else {
			seenNames[r.Name] = r.FilePath
		}
		if prev, dup := seenFiles[r.FileName]; dup {
			errs = append(errs, fmt.Errorf("duplicate routing name '%s' (from '%s' and '%s'): /webhook/%s is ambiguous", r.FileName, prev, r.FilePath, r.FileName))
		} else {
			seenFiles[r.FileName] = r.FilePath
		}
	}

	return errs
}

// RuleCount returns the number of loaded rule config files.
func (m *Manager) RuleCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rules)
}

// FindByFileName returns the rule config matching the given file name (without extension).
func (m *Manager) FindByFileName(fileName string) *RuleConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.rules {
		if r.FileName == fileName {
			return r
		}
	}
	return nil
}

// loadGlobalConfig reads and parses config.yaml.
func loadGlobalConfig(path string) (GlobalConfig, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Use defaults if config file doesn't exist
			return cfg, cfg.Validate()
		}
		return cfg, fmt.Errorf("read config file: %w", err)
	}

	// Resolve ${env:} and ${file:} references before parsing
	resolved, err := interpolate(string(data))
	if err != nil {
		return cfg, fmt.Errorf("resolve secrets in '%s': %w", path, err)
	}

	if err := yaml.Unmarshal([]byte(resolved), &cfg); err != nil {
		return cfg, fmt.Errorf("parse config file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// loadRuleConfigs reads and parses all *.yaml files in the given directory.
func loadRuleConfigs(dir string) ([]*RuleConfig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no hooks directory is OK
		}
		return nil, fmt.Errorf("read hooks directory: %w", err)
	}

	var rules []*RuleConfig
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".yaml") && !strings.HasSuffix(strings.ToLower(name), ".yml") {
			continue
		}

		filePath := filepath.Join(dir, name)
		rc, err := loadRuleFile(filePath)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rc)
	}

	return rules, nil
}

// loadRuleFile reads and parses a single rule YAML file.
func loadRuleFile(path string) (*RuleConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rule file '%s': %w", path, err)
	}

	// Resolve ${env:} and ${file:} references before parsing
	resolved, err := interpolate(string(data))
	if err != nil {
		return nil, fmt.Errorf("resolve secrets in '%s': %w", path, err)
	}

	var rc RuleConfig
	if err := yaml.Unmarshal([]byte(resolved), &rc); err != nil {
		return nil, fmt.Errorf("parse rule file '%s': %w", path, err)
	}

	rc.FilePath = path

	// Extract filename without extension for routing
	ext := filepath.Ext(path)
	rc.FileName = strings.TrimSuffix(filepath.Base(path), ext)

	if err := rc.Validate(); err != nil {
		return nil, err
	}

	return &rc, nil
}
