package tools

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestRunMediaCollector_AddAndDrain(t *testing.T) {
	c := NewRunMediaCollector()
	c.Add("/workspace/file.png", "image/png")
	c.Add("/workspace/doc.pdf", "application/pdf")

	items := c.Drain()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Path != "/workspace/file.png" {
		t.Errorf("expected path /workspace/file.png, got %s", items[0].Path)
	}
	if items[1].ContentType != "application/pdf" {
		t.Errorf("expected content type application/pdf, got %s", items[1].ContentType)
	}

	// Drain again should be empty
	items = c.Drain()
	if len(items) != 0 {
		t.Fatalf("expected 0 items after second drain, got %d", len(items))
	}
}

func TestRunMediaCollector_ThreadSafety(t *testing.T) {
	c := NewRunMediaCollector()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.Add(fmt.Sprintf("/file_%d.png", n), "image/png")
		}(i)
	}
	wg.Wait()
	items := c.Drain()
	if len(items) != 100 {
		t.Fatalf("expected 100 items, got %d", len(items))
	}
}

func TestRunMediaCollector_ContextRoundtrip(t *testing.T) {
	ctx := context.Background()
	if RunMediaCollectorFromCtx(ctx) != nil {
		t.Fatal("expected nil from empty context")
	}

	c := NewRunMediaCollector()
	ctx = WithRunMediaCollector(ctx, c)
	got := RunMediaCollectorFromCtx(ctx)
	if got != c {
		t.Fatal("expected same collector from context")
	}
}

func TestIsInternalChannel(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"ws", true},
		{"cli", true},
		{"system", true},
		{"subagent", true},
		{"browser", true},
		{"telegram", false},
		{"discord", false},
		{"slack", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isInternalChannel(tt.name); got != tt.want {
			t.Errorf("isInternalChannel(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
