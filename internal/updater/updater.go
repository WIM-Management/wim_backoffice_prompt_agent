// Package updater self-updates the agent binary from the public releases repo.
package updater

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const releasesRepo = "WIM-Management/wim_backoffice_prompt_agent_releases"

// apiBase is a var so tests can point it at httptest.
var apiBase = "https://api.github.com"

// latestVersion returns the newest published release tag (e.g. "v0.5.0").
func latestVersion() (string, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", apiBase, releasesRepo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	hc := &http.Client{Timeout: 10 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("releases/latest http %d", resp.StatusCode)
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", fmt.Errorf("empty tag_name")
	}
	return r.TagName, nil
}
