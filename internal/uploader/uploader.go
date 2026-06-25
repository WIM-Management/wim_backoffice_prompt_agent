package uploader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
)

// TokenFn is a provider that returns a Bearer token (or error).
type TokenFn func() (string, error)

type Uploader struct {
	base  string
	token TokenFn
	batch int
	hc    *http.Client
}

func New(base string, token TokenFn, batch int) *Uploader {
	if batch <= 0 {
		batch = 100
	}
	return &Uploader{base, token, batch, &http.Client{Timeout: 30 * time.Second}}
}

func (u *Uploader) Send(evs []model.Event) error {
	for i := 0; i < len(evs); i += u.batch {
		end := i + u.batch
		if end > len(evs) {
			end = len(evs)
		}
		if err := u.sendBatch(evs[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (u *Uploader) sendBatch(evs []model.Event) error {
	tok, err := u.token()
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"events": evs})
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequest(http.MethodPatch, u.base+"/api/v1/prompt-insights/events", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := u.hc.Do(req)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		lastErr = fmt.Errorf("attempt %d: status=%v err=%v", attempt, func() int {
			if resp != nil {
				return resp.StatusCode
			}
			return 0
		}(), err)
		if attempt < 4 {
			time.Sleep(time.Duration(1<<attempt) * time.Second) // 1,2,4,8s
		}
	}
	return lastErr
}
