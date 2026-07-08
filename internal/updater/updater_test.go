package updater

import (
	"net/http"
	"net/http/httptest"
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
