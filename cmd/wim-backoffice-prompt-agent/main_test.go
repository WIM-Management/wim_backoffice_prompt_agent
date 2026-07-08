package main

import (
	"testing"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/enroll"
)

func TestNeedsEnroll(t *testing.T) {
	panicVerify := func() enroll.TokenValidity {
		t.Fatal("verify must not be called when token is empty")
		return enroll.TokenUnknown
	}
	constVerify := func(v enroll.TokenValidity) func() enroll.TokenValidity {
		return func() enroll.TokenValidity { return v }
	}

	cases := []struct {
		name   string
		token  string
		verify func() enroll.TokenValidity
		want   bool
	}{
		{"empty token → enroll (no verify call)", "", panicVerify, true},
		{"valid token → skip", "tok", constVerify(enroll.TokenValid), false},
		{"rejected token → enroll", "tok", constVerify(enroll.TokenRejected), true},
		{"unknown (offline/5xx) → skip, keep healthy token", "tok", constVerify(enroll.TokenUnknown), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := needsEnroll(c.token, c.verify); got != c.want {
				t.Errorf("needsEnroll = %v, want %v", got, c.want)
			}
		})
	}
}
