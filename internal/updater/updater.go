// Package updater self-updates the agent binary from the public releases repo.
package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releasesRepo = "WIM-Management/wim_backoffice_prompt_agent_releases"

const maxAssetBytes = 200 << 20 // 200 MB

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

// dlBase is a var so tests can point downloads at httptest.
var dlBase = "https://github.com"

// assetName returns the release asset filename for this platform,
// matching the release CI naming exactly.
func assetName() string {
	name := fmt.Sprintf("wim-backoffice-prompt-agent-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// downloadAndVerify fetches the platform asset + SHA256SUMS from the latest
// release, writes the asset to a temp file in destDir, and verifies its hash.
// On failure it returns before creating the temp file, so a failed download or
// checksum leaves no artifact and never touches the caller's binary.
func downloadAndVerify(destDir string) (string, error) {
	base := fmt.Sprintf("%s/%s/releases/latest/download", dlBase, releasesRepo)
	asset := assetName()

	sums, err := httpGetBytes(base + "/SHA256SUMS")
	if err != nil {
		return "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want, err := sumFor(string(sums), asset)
	if err != nil {
		return "", err
	}

	binBytes, err := httpGetBytes(base + "/" + asset)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	got := sha256.Sum256(binBytes)
	if hex.EncodeToString(got[:]) != want {
		return "", fmt.Errorf("checksum mismatch for %s", asset)
	}

	tmp := filepath.Join(destDir, asset+".new")
	if err := os.WriteFile(tmp, binBytes, 0o755); err != nil {
		return "", err
	}
	return tmp, nil
}

func httpGetBytes(url string) ([]byte, error) {
	hc := &http.Client{Timeout: 60 * time.Second}
	resp, err := hc.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes))
}

// sumFor finds the hex digest for asset in SHA256SUMS content ("<hex>  <name>").
func sumFor(sums, asset string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == asset {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", asset)
}

// Result describes the outcome of a self-update attempt.
type Result struct {
	Updated bool
	From    string
	To      string
}

// CheckAndUpdate compares the running version to the latest release and, if
// newer, downloads+verifies+replaces the binary at execPath. A "dev" build is
// skipped. The temp download lives beside execPath so the final rename is atomic.
func CheckAndUpdate(currentVersion, execPath string) (Result, error) {
	if currentVersion == "dev" {
		return Result{}, nil
	}
	latest, err := latestVersion()
	if err != nil {
		return Result{}, err
	}
	if !isNewer(latest, currentVersion) {
		return Result{Updated: false, From: currentVersion, To: latest}, nil
	}
	tmp, err := downloadAndVerify(filepath.Dir(execPath))
	if err != nil {
		return Result{}, err
	}
	if err := replaceBinary(tmp, execPath); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	return Result{Updated: true, From: currentVersion, To: latest}, nil
}

// isNewer reports whether release tag `latest` is a strictly newer semver than
// `current`. Both may carry a leading "v"; pre-release/build metadata is ignored.
// Guards against a needless re-download on equal versions and a backward
// "update" if releases/latest ever points at an older tag (yank/re-point).
func isNewer(latest, current string) bool {
	l, c := parseSemver(latest), parseSemver(current)
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
