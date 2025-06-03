package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"roller/config"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(cfg *config.Config, token string) *Client {
	return &Client{
		baseURL: cfg.GitlabURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("PRIVATE-TOKEN", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	return resp, nil
}

// BaseURL returns the base URL of the GitLab instance
func (c *Client) BaseURL() string {
	return c.baseURL
}

func FetchGroupProjects(ctx context.Context, client *Client, group string) ([]config.RepoSpec, error) {
	path := fmt.Sprintf("/api/v4/groups/%s/projects?per_page=100", group)
	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitLab API error: %s", string(body))
	}

	var projects []struct {
		PathWithNamespace string `json:"path_with_namespace"`
		Archived          bool   `json:"archived"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
		return nil, err
	}

	var repos []config.RepoSpec
	for _, p := range projects {
		if p.Archived {
			continue
		}
		repos = append(repos, config.RepoSpec{
			RepoPath: p.PathWithNamespace,
			RoleName: "", // Will be detected during clone
		})
	}

	return repos, nil
}
