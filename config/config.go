package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// RepoSpec represents a GitLab repository specification with its path and role
type RepoSpec struct {
	RepoPath string `yaml:"path"`
	RoleName string `yaml:"role"`
}

// Config represents the application's configuration structure
type Config struct {
	GitlabURL     string            `yaml:"gitlab_url"`
	FeatureBranch string            `yaml:"feature_branch"`
	TargetBranch  string            `yaml:"target_branch"`
	AnsibleRoles  map[string]string `yaml:"ansible_roles"`
	Projects      []RepoSpec        `yaml:"projects"`
	AutoDiscover  struct {
		Group string `yaml:"group"`
	} `yaml:"auto_discover"`
	Cleanup bool `yaml:"cleanup"` // Whether to clean up cloned repositories after processing
}

// Validate checks if the configuration is valid and returns all validation errors
func (c *Config) Validate() error {
	var errs []string

	if c.GitlabURL == "" {
		errs = append(errs, "gitlab_url is required")
	} else if !strings.HasPrefix(c.GitlabURL, "http://") && !strings.HasPrefix(c.GitlabURL, "https://") {
		errs = append(errs, "gitlab_url must start with http:// or https://")
	}

	if c.FeatureBranch == "" {
		errs = append(errs, "feature_branch is required")
	}
	if c.TargetBranch == "" {
		errs = append(errs, "target_branch is required")
	}
	if c.AutoDiscover.Group == "" {
		errs = append(errs, "auto_discover.group is required")
	}

	if len(errs) > 0 {
		return errors.New("validation errors:\n - " + strings.Join(errs, "\n - "))
	}
	return nil
}

// LoadConfig reads and parses the configuration file from the given path
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var c Config
	if err = yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &c, nil
}

// ExportDiscoveredProjects writes the discovered projects to a YAML file
func ExportDiscoveredProjects(path string, projects []RepoSpec) error {
	if len(projects) == 0 {
		return fmt.Errorf("no projects to export")
	}

	out := struct {
		Projects []RepoSpec `yaml:"projects"`
	}{
		Projects: projects,
	}

	data, err := yaml.Marshal(out)
	if err != nil {
		return fmt.Errorf("failed to marshal projects: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write projects file: %w", err)
	}

	return nil
}
