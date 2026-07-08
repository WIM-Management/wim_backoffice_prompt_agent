package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLatestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/WIM-Management/wim_backoffice_prompt_agent_releases/releases/latest" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"tag_name":"v0.5.0","name":"v0.5.0"}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	got, err := latestVersion()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "v0.5.0" {
		t.Errorf("latestVersion = %q, want v0.5.0", got)
	}
}

func TestLatestVersionHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	if _, err := latestVersion(); err == nil {
		t.Error("expected error on 500")
	}
}

func TestDownloadAndVerifyChecksumMismatch(t *testing.T) {
	body := []byte("new-binary-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case filepath.Base(r.URL.Path) == "SHA256SUMS":
			// 틀린 해시 → 불일치
			fmt.Fprintf(w, "%s  %s\n", "deadbeef", assetName())
		default:
			w.Write(body)
		}
	}))
	defer srv.Close()
	old := dlBase
	dlBase = srv.URL
	defer func() { dlBase = old }()

	if _, err := downloadAndVerify(t.TempDir()); err == nil {
		t.Error("expected checksum mismatch error")
	}
}

func TestDownloadAndVerifyOK(t *testing.T) {
	body := []byte("new-binary-bytes")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Base(r.URL.Path) == "SHA256SUMS" {
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), assetName())
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	old := dlBase
	dlBase = srv.URL
	defer func() { dlBase = old }()

	tmp, err := downloadAndVerify(t.TempDir())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got, _ := os.ReadFile(tmp)
	if string(got) != string(body) {
		t.Errorf("tmp content mismatch")
	}
}

func TestReplaceBinaryUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "agent")
	tmp := filepath.Join(dir, "agent.new")
	os.WriteFile(exe, []byte("old"), 0o755)
	os.WriteFile(tmp, []byte("new"), 0o644)
	if err := replaceBinary(tmp, exe); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "new" {
		t.Errorf("exe = %q, want new", got)
	}
	info, _ := os.Stat(exe)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("perm = %v, want 0755", info.Mode().Perm())
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.5.0", "v0.4.2", true},
		{"v0.4.2", "v0.4.2", false},        // 동일 → no-op
		{"v0.4.1", "v0.4.2", false},        // 더 낮음 → 다운그레이드 금지
		{"0.5.0", "v0.5.0", false},         // v 접두 정규화 → 동일
		{"v1.0.0", "v0.9.9", true},
		{"v0.10.0", "v0.9.0", true},        // 숫자 비교(문자열 아님)
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q,%q)=%v want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestCheckAndUpdateDevSkips(t *testing.T) {
	r, err := CheckAndUpdate("dev", "/nonexistent")
	if err != nil || r.Updated {
		t.Errorf("dev build must skip: r=%+v err=%v", r, err)
	}
}

func TestCheckAndUpdateAlreadyLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v1.0.0"}`))
	}))
	defer srv.Close()
	oldA := apiBase
	apiBase = srv.URL
	defer func() { apiBase = oldA }()

	r, err := CheckAndUpdate("v1.0.0", "/nonexistent")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Updated {
		t.Errorf("already latest must not update: %+v", r)
	}
}

func TestCheckAndUpdatePerformsUpgrade(t *testing.T) {
	body := []byte("v2-binary")
	sum := sha256.Sum256(body)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/WIM-Management/wim_backoffice_prompt_agent_releases/releases/latest",
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"tag_name":"v2.0.0"}`)) })
	mux.HandleFunc("/WIM-Management/wim_backoffice_prompt_agent_releases/releases/latest/download/"+assetName(),
		func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	mux.HandleFunc("/WIM-Management/wim_backoffice_prompt_agent_releases/releases/latest/download/SHA256SUMS",
		func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), assetName())
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	oldA, oldD := apiBase, dlBase
	apiBase, dlBase = srv.URL, srv.URL
	defer func() { apiBase, dlBase = oldA, oldD }()

	dir := t.TempDir()
	exe := filepath.Join(dir, "agent")
	os.WriteFile(exe, []byte("v1-binary"), 0o755)

	r, err := CheckAndUpdate("v1.0.0", exe)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !r.Updated || r.To != "v2.0.0" {
		t.Errorf("expected upgrade to v2.0.0, got %+v", r)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "v2-binary" {
		t.Errorf("exe not replaced: %q", got)
	}
}
