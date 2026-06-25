// Package enroll handles device onboarding: Google id_token → backend enroll →
// prompt-agent device token, stored in the OS keychain.
package enroll

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// TokenStore persists the device token (OS keychain in production, memory in tests).
type TokenStore interface {
	Get() (string, error)
	Set(string) error
}

// IDTokenFn obtains a Google id_token via the native OAuth flow.
type IDTokenFn func() (string, error)

// Enroller posts the id_token to backend enroll and stores the returned device token.
type Enroller struct {
	base    string
	store   TokenStore
	idToken IDTokenFn
	hc      *http.Client
}

func New(base string, store TokenStore, idToken IDTokenFn) *Enroller {
	return &Enroller{base, store, idToken, http.DefaultClient}
}

// Run enrolls this device. issuePromptAgentToken=true asks backend to mint the
// collection token (server still gates on Google id_token → employee).
func (e *Enroller) Run(label string) error {
	idt, err := e.idToken()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"idToken":               idt,
		"label":                 label,
		"issuePromptAgentToken": true,
	})
	resp, err := e.hc.Post(e.base+"/api/v1/prompt-insights/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("enroll http %d", resp.StatusCode)
	}
	var r struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if r.Token == "" {
		return fmt.Errorf("enroll returned empty token (issuePromptAgentToken not honored?)")
	}
	return e.store.Set(r.Token)
}

// Token returns the stored device token (used as the uploader's TokenFn).
func (e *Enroller) Token() (string, error) { return e.store.Get() }

// MemStore is an in-memory TokenStore for tests.
type MemStore struct{ v string }

func NewMemStore() *MemStore             { return &MemStore{} }
func (m *MemStore) Get() (string, error) { return m.v, nil }
func (m *MemStore) Set(v string) error   { m.v = v; return nil }
