package uploader_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/uploader"
)

func TestUploaderSend(t *testing.T) {
	var gotAuth string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"accepted":1,"duplicate":0}`))
	}))
	defer srv.Close()

	u := uploader.New(srv.URL, func() (string, error) { return "tok", nil }, 100)
	err := u.Send([]model.Event{{SourceTool: "CLAUDE_CODE", Surface: "cli", PromptText: "hi", ClientVersion: "0.1.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth %q", gotAuth)
	}
	if !strings.Contains(string(body), `"promptText":"hi"`) {
		t.Fatalf("body %s", body)
	}
}

func TestUploaderSendBatchSplit(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"accepted":1,"duplicate":0}`))
	}))
	defer srv.Close()

	u := uploader.New(srv.URL, func() (string, error) { return "tok", nil }, 2)
	evs := []model.Event{
		{PromptText: "a"},
		{PromptText: "b"},
		{PromptText: "c"},
	}
	if err := u.Send(evs); err != nil {
		t.Fatal(err)
	}
	// 3 events / batch=2 → 2 calls
	if callCount != 2 {
		t.Fatalf("want 2 PATCH calls got %d", callCount)
	}
}

func TestUploaderMethodIsPatch(t *testing.T) {
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"accepted":1,"duplicate":0}`))
	}))
	defer srv.Close()

	u := uploader.New(srv.URL, func() (string, error) { return "tok", nil }, 100)
	_ = u.Send([]model.Event{{PromptText: "x"}})
	if method != http.MethodPatch {
		t.Fatalf("want PATCH got %s", method)
	}
}
