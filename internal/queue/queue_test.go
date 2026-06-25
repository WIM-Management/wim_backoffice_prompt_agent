package queue_test

import (
	"testing"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/queue"
)

func TestQueuePersists(t *testing.T) {
	dir := t.TempDir()
	q1 := queue.New(dir)
	_ = q1.Enqueue([]model.Event{{PromptText: "a"}})
	q2 := queue.New(dir) // 재시작 시뮬
	var got int
	_ = q2.Drain(func(b []model.Event) error { got += len(b); return nil })
	if got != 1 {
		t.Fatalf("persisted want 1 got %d", got)
	}
}

func TestQueueDrainDeletesOnSuccess(t *testing.T) {
	dir := t.TempDir()
	q := queue.New(dir)
	_ = q.Enqueue([]model.Event{{PromptText: "x"}, {PromptText: "y"}})

	var got int
	_ = q.Drain(func(b []model.Event) error { got += len(b); return nil })
	if got != 2 {
		t.Fatalf("want 2 got %d", got)
	}

	// After successful drain, files should be gone — second drain yields 0
	got = 0
	_ = q.Drain(func(b []model.Event) error { got += len(b); return nil })
	if got != 0 {
		t.Fatalf("after drain want 0 got %d", got)
	}
}

func TestQueueEnqueueMultipleBatches(t *testing.T) {
	dir := t.TempDir()
	q := queue.New(dir)
	_ = q.Enqueue([]model.Event{{PromptText: "1"}})
	_ = q.Enqueue([]model.Event{{PromptText: "2"}, {PromptText: "3"}})

	var got int
	_ = q.Drain(func(b []model.Event) error { got += len(b); return nil })
	if got != 3 {
		t.Fatalf("want 3 got %d", got)
	}
}
