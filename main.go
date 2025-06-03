package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"time"

	"roller/config"
	"roller/gitlab"
)

// detectRepoType checks for common dependency files in the repository
func detectRepoType(repoPath string) (string, error) {
	// Check for common dependency files
	dependencyFiles := map[string]bool{
		"pom.xml":          false,
		"requirements.txt": false,
		"package.json":     false,
	}

	// Walk through the repository directory
	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip the .git directory
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		// Check if the file is one of our dependency files
		if !info.IsDir() {
			if _, exists := dependencyFiles[info.Name()]; exists {
				dependencyFiles[info.Name()] = true
			}
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("error scanning repository: %w", err)
	}

	// Determine the role based on found files
	switch {
	case dependencyFiles["pom.xml"]:
		return "pom", nil
	case dependencyFiles["requirements.txt"]:
		return "pip", nil
	case dependencyFiles["package.json"]:
		return "node", nil
	default:
		return "", fmt.Errorf("no supported package manager found")
	}
}

// cloneAndCreateBranch clones a single project into "repos/<name>" and creates a feature branch.
// Returns an error if anything fails.
func cloneAndCreateBranch(ctx context.Context, token, baseURL, targetBranch, featureBranch, repoPath string) error {
	// Compute clone URL using net/url parsing
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid GitLab URL %q: %w", baseURL, err)
	}
	// Ensure scheme is https
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	parsed.User = url.UserPassword("oauth2", token)
	parsed.Path = path.Join(parsed.Path, repoPath) + ".git"
	cloneURL := parsed.String()

	repoName := path.Base(repoPath) // e.g., "myrepo" from "group/subgroup/myrepo"
	destDir := filepath.Join("repos", repoName)

	log.Printf("📥 Cloning %s into %s (branch: %s)", repoPath, destDir, targetBranch)
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", targetBranch, cloneURL, destDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed for %s: %w", repoPath, err)
	}

	// Now create & checkout the feature branch
	log.Printf("✨ Checking out feature branch %s in %s", featureBranch, destDir)
	cmd = exec.CommandContext(ctx, "git", "checkout", "-b", featureBranch)
	cmd.Dir = destDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git checkout -b %s failed in %s: %w", featureBranch, destDir, err)
	}

	// Detect repository type
	role, err := detectRepoType(destDir)
	if err != nil {
		log.Printf("⚠️  Warning: Could not detect repository type for %s: %v", repoPath, err)
	} else {
		log.Printf("📦 Repository type for %s: %s", repoPath, role)
	}

	log.Printf("✅ Successfully prepared %s (feature: %s)", repoPath, featureBranch)

	// Run Ansible playbook
	log.Printf("🔧 Running Ansible playbook for %s", repoPath)
	cmd = exec.CommandContext(ctx, "ansible-playbook", filepath.Join("ansible", "site.yml"))
	cmd.Dir = "." // Run from the workspace root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("⚠️  Warning: Ansible playbook execution failed for %s: %v", repoPath, err)
	} else {
		log.Printf("✅ Successfully ran Ansible playbook for %s", repoPath)
	}

	return nil
}

// discoverAndExportProjects performs auto-discovery, determines roles, and exports to YAML
func discoverAndExportProjects(ctx context.Context, client *gitlab.Client, group string, outputPath string) error {
	// Fetch projects from GitLab group
	log.Printf("🔍 Fetching projects from group: %s", group)
	projects, err := gitlab.FetchGroupProjects(ctx, client, group)
	if err != nil {
		return fmt.Errorf("failed to fetch projects: %w", err)
	}

	// Create temporary directory for cloning
	tempDir := filepath.Join("repos", "temp")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir) // Clean up temp directory

	// Process each project to determine its role
	for i, proj := range projects {
		// Clone the repository
		cloneURL := fmt.Sprintf("%s/%s.git", client.BaseURL(), proj.RepoPath)
		repoName := path.Base(proj.RepoPath)
		destDir := filepath.Join(tempDir, repoName)

		log.Printf("📥 Cloning %s to detect role", proj.RepoPath)
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", cloneURL, destDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Printf("⚠️  Warning: Failed to clone %s: %v", proj.RepoPath, err)
			continue
		}

		// Detect role
		role, err := detectRepoType(destDir)
		if err != nil {
			log.Printf("⚠️  Warning: Could not detect role for %s: %v", proj.RepoPath, err)
			continue
		}

		// Update project with detected role
		projects[i].RoleName = role
		log.Printf("✅ Detected role for %s: %s", proj.RepoPath, role)
	}

	// Export projects to YAML
	if err := config.ExportDiscoveredProjects(outputPath, projects); err != nil {
		return fmt.Errorf("failed to export projects: %w", err)
	}

	log.Printf("✅ Successfully exported %d projects to %s", len(projects), outputPath)
	return nil
}

func main() {
	// Parse command line flags
	discoverFlag := flag.Bool("discover", false, "Run in discovery mode to detect roles and export to YAML")
	outputFlag := flag.String("output", "discovered_projects.yaml", "Output file for discovered projects (used with -discover)")
	flag.Parse()

	// 1. Load config: bail out immediately if it fails
	cfg, err := config.LoadConfig("roller.yaml")
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	// 2. Validate essential config fields
	if cfg.GitlabURL == "" {
		log.Fatal("config: gitlab_url is required")
	}
	if cfg.TargetBranch == "" {
		log.Fatal("config: target_branch is required")
	}
	if cfg.FeatureBranch == "" {
		log.Fatal("config: feature_branch is required")
	}

	// 3. Get token from env
	token := os.Getenv("GITLAB_TOKEN")
	if token == "" {
		log.Fatal("GITLAB_TOKEN environment variable is required")
	}

	// 4. Initialize GitLab client
	client := gitlab.NewClient(cfg, token)

	// If in discovery mode, run discovery and exit
	if *discoverFlag {
		if cfg.AutoDiscover == nil || cfg.AutoDiscover.Group == "" {
			log.Fatal("auto_discover.group must be specified in config for discovery mode")
		}
		ctx := context.Background()
		if err := discoverAndExportProjects(ctx, client, cfg.AutoDiscover.Group, *outputFlag); err != nil {
			log.Fatalf("Discovery failed: %v", err)
		}
		return
	}

	// 5. Fetch auto-discovered projects (if configured)
	ctx := context.Background()
	var autoProjects []config.RepoSpec
	if cfg.AutoDiscover != nil && cfg.AutoDiscover.Group != "" {
		log.Printf("🔍 Fetching auto-discovered projects from group: %s", cfg.AutoDiscover.Group)
		autoProjects, err = gitlab.FetchGroupProjects(ctx, client, cfg.AutoDiscover.Group)
		if err != nil {
			log.Fatalf("Failed to fetch projects from group %s: %v", cfg.AutoDiscover.Group, err)
		}
	}

	// 6. Merge manually specified projects + auto-discovered
	allProjects := append(cfg.Projects, autoProjects...)
	if len(allProjects) == 0 {
		log.Fatal("No projects to process (check config.projects or config.auto_discover.group)")
	}

	// 7. Create base "repos" directory once
	reposDir := "repos"
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		log.Fatalf("Failed to create directory %q: %v", reposDir, err)
	}

	// 8. Set up a per-clone timeout: e.g., 2 minutes per repo

	for _, proj := range allProjects {
		// Create a child context with timeout
		cloneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := cloneAndCreateBranch(cloneCtx, token, cfg.GitlabURL, cfg.TargetBranch, cfg.FeatureBranch, proj.RepoPath)
		cancel()

		if err != nil {
			// Here we simply log and continue. You could accumulate errors if you want.
			log.Printf("⚠️  Error processing %s: %v", proj.RepoPath, err)
		}
	}
}
