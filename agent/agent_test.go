package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunSwarm_PreservesOrder(t *testing.T) {
	s := NewScheduler(4)
	tasks := []Task{{ID: "a", Input: "1"}, {ID: "b", Input: "2"}, {ID: "c", Input: "3"}}
	results := s.RunSwarm(context.Background(), tasks, func(ctx context.Context, task Task) Result {
		return Result{TaskID: task.ID, Output: "processed:" + task.Input}
	})
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	want := []string{"a", "b", "c"}
	for i, r := range results {
		if r.TaskID != want[i] {
			t.Errorf("results[%d].TaskID = %q, want %q (orden posicional, no de finalización)", i, r.TaskID, want[i])
		}
	}
}

func TestRunSwarm_RespectsMaxConcurrency(t *testing.T) {
	s := NewScheduler(2)
	var current, max int64

	tasks := make([]Task, 10)
	for i := range tasks {
		tasks[i] = Task{ID: string(rune('a' + i)), Input: "x"}
	}

	s.RunSwarm(context.Background(), tasks, func(ctx context.Context, task Task) Result {
		n := atomic.AddInt64(&current, 1)
		for {
			m := atomic.LoadInt64(&max)
			if n <= m || atomic.CompareAndSwapInt64(&max, m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&current, -1)
		return Result{TaskID: task.ID}
	})

	if max > 2 {
		t.Fatalf("concurrencia observada = %d, want <= 2 (MaxConcurrency)", max)
	}
	if max < 2 {
		t.Fatalf("concurrencia observada = %d, want == 2 (el scheduler debería usar todo el presupuesto disponible)", max)
	}
}

func TestNewScheduler_ClampsNonPositive(t *testing.T) {
	if s := NewScheduler(0); s.MaxConcurrency != 1 {
		t.Errorf("MaxConcurrency = %d, want 1", s.MaxConcurrency)
	}
	if s := NewScheduler(-5); s.MaxConcurrency != 1 {
		t.Errorf("MaxConcurrency = %d, want 1", s.MaxConcurrency)
	}
}
