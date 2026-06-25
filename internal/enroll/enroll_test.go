package enroll_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/enroll"
)

func TestEnrollStoresToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/prompt-insights/enroll" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"token":"ptk","deviceId":"d","liteLlmKey":"k"}`))
	}))
	defer srv.Close()

	store := enroll.NewMemStore()
	e := enroll.New(srv.URL, store, func() (string, error) { return "idtok", nil })
	if err := e.Run("macbook"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get()
	if got != "ptk" {
		t.Fatalf("token = %q, want ptk", got)
	}
}

func TestEnrollEmptyTokenErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"token":"","deviceId":"d"}`))
	}))
	defer srv.Close()

	e := enroll.New(srv.URL, enroll.NewMemStore(), func() (string, error) { return "idtok", nil })
	if err := e.Run("x"); err == nil {
		t.Fatal("expected error on empty token, got nil")
	}
}
