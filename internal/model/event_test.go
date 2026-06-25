package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEvent_PromptTsNaiveUTC(t *testing.T) {
	e := Event{PromptText: "hi", PromptTs: NaiveTS(time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)), Surface: "cli", ClientVersion: "0.1.0", SourceTool: "CLAUDE_CODE"}
	b, _ := json.Marshal(e)
	if !strings.Contains(string(b), `"promptTs":"2026-06-24T09:00:00"`) {
		t.Fatalf("promptTs not naive UTC: %s", b)
	}
}
