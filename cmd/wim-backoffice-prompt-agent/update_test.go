package main

import (
	"testing"
	"time"
)

func TestShouldCheckUpdate(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		want bool
	}{
		{"zero(첫 실행)", time.Time{}, true},
		{"23h 전 → 아직", now.Add(-23 * time.Hour), false},
		{"24h 전 → 체크", now.Add(-24 * time.Hour), true},
		{"48h 전 → 체크", now.Add(-48 * time.Hour), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldCheckUpdate(c.last, now); got != c.want {
				t.Errorf("shouldCheckUpdate = %v, want %v", got, c.want)
			}
		})
	}
}
