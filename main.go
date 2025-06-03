package main

import (
	"context"
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

// RepoType represents the type of project based on its dependency files
type RepoType struct {
	IsJava          bool
	IsPython        bool
	IsNode          bool
	DependencyFiles []string
}

// detectRepoType checks for common dependency files in the repository
func detectRepoType(repoPath string) (RepoType, error) {
	var rt RepoType
	var files []string

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
				files = append(files, info.Name())
			}
		}
		return nil
	})

	if err != nil {
		return rt, fmt.Errorf("error scanning repository: %w", err)
	}

	// Set the repo type based on found files
	rt.IsJava = dependencyFiles["pom.xml"]
	rt.IsPython = dependencyFiles["requirements.txt"]
	rt.IsNode = dependencyFiles["package.json"]
	rt.DependencyFiles = files

	return rt, nil
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
	repoType, err := detectRepoType(destDir)
	if err != nil {
		log.Printf("⚠️  Warning: Could not detect repository type for %s: %v", repoPath, err)
	} else {
		log.Printf("📦 Repository type for %s:", repoPath)
		if repoType.IsJava {
			log.Printf("   - Java (Maven)")
		}
		if repoType.IsPython {
			log.Printf("   - Python")
		}
		if repoType.IsNode {
			log.Printf("   - Node.js")
		}
		if len(repoType.DependencyFiles) > 0 {
			log.Printf("   - Found dependency files: %v", repoType.DependencyFiles)
		}
	}

	log.Printf("✅ Successfully prepared %s (feature: %s)", repoPath, featureBranch)
	return nil
}

func main() {
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

	// 5. Fetch auto-discovered projects (if configured)
	ctx := context.Background()
	var autoProjects []config.RepoSpec
	if cfg.AutoDiscover.Group != "" {
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
	//    You could also add a global timeout; for now, do it per invocation.
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
