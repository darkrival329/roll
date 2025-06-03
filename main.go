package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"roller/config"
	"roller/gitlab"
	"strings"
)

func main() {
	// Load configuration
	cfg, err := config.LoadConfig("roller.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Get GitLab token from environment
	token := os.Getenv("GITLAB_TOKEN")
	if token == "" {
		log.Fatal("GITLAB_TOKEN environment variable is required")
	}

	// Initialize GitLab client
	client := gitlab.NewClient(cfg, token)

	// Fetch auto-discovered projects
	projects, err := gitlab.FetchGroupProjects(context.Background(), client, cfg.AutoDiscover.Group)
	if err != nil {
		log.Fatalf("Failed to fetch group projects: %v", err)
	}

	// Combine manually specified and auto-discovered projects
	allProjects := append(cfg.Projects, projects...)

	// Check if there are any projects to process
	if len(allProjects) == 0 {
		log.Fatal("No projects to process (check config.projects or auto_discover.group)")
	}

	// Create repos directory
	if err := os.MkdirAll("repos", 0755); err != nil {
		log.Fatalf("Failed to create repos directory: %v", err)
	}

	// Clone each project
	for _, proj := range allProjects {
		// Remove https:// from the base URL if it exists
		baseURL := cfg.GitlabURL
		if len(baseURL) > 8 && baseURL[:8] == "https://" {
			baseURL = baseURL[8:]
		}
		cloneURL := fmt.Sprintf("https://oauth2:%s@%s/%s.git", token, baseURL, proj.RepoPath)
		log.Printf("Cloning %s (branch: %s)...", proj.RepoPath, cfg.TargetBranch)

		// Clone the target branch
		cmd := exec.Command("git", "clone", "--depth", "1", "--branch", cfg.TargetBranch, cloneURL)
		cmd.Dir = "repos"
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Failed to clone %s: %v\nOutput: %s", proj.RepoPath, err, string(output))
			continue
		}

		// Get the repository name from the path (last part after /)
		repoName := proj.RepoPath[strings.LastIndex(proj.RepoPath, "/")+1:]
		repoPath := filepath.Join("repos", repoName)

		// Create and checkout the feature branch
		cmd = exec.Command("git", "checkout", "-b", cfg.FeatureBranch)
		cmd.Dir = repoPath
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Failed to create feature branch for %s: %v\nOutput: %s", proj.RepoPath, err, string(output))
			continue
		}

		log.Printf("Successfully cloned %s and created feature branch %s", proj.RepoPath, cfg.FeatureBranch)
	}
}
