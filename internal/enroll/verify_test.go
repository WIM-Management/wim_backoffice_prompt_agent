package enroll

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyToken(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   TokenValidity
	}{
		{"200 valid", http.StatusOK, TokenValid},
		{"401 rejected", http.StatusUnauthorized, TokenRejected},
		{"403 rejected", http.StatusForbidden, TokenRejected},
		{"500 unknown", http.StatusInternalServerError, TokenUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				if r.URL.Path != "/api/v1/prompt-insights/cursor" {
					t.Errorf("path = %s", r.URL.Path)
				}
				w.WriteHeader(c.status)
			}))
			defer srv.Close()
			if got := VerifyToken(srv.URL, "tok123"); got != c.want {
				t.Errorf("VerifyToken = %v, want %v", got, c.want)
			}
			if gotAuth != "Bearer tok123" {
				t.Errorf("Authorization = %q", gotAuth)
			}
		})
	}
}

func TestVerifyTokenNetworkErrorIsUnknown(t *testing.T) {
	// 닫힌 서버 주소 → 연결 실패 → Unknown
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	if got := VerifyToken(url, "tok"); got != TokenUnknown {
		t.Errorf("network error = %v, want TokenUnknown", got)
	}
}
