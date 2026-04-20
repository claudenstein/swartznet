package testlab

import (
	"strings"
	"sync"
	"testing"
)

func TestJoinLinesAppendsNewlines(t *testing.T) {
	t.Parallel()
	got := joinLines([]string{"a", "b", "c"})
	want := "a\nb\nc\n"
	if got != want {
		t.Errorf("joinLines = %q, want %q", got, want)
	}
}

func TestJoinLinesEmptyInput(t *testing.T) {
	t.Parallel()
	if got := joinLines(nil); got != "" {
		t.Errorf("joinLines(nil) = %q, want empty", got)
	}
	if got := joinLines([]string{}); got != "" {
		t.Errorf("joinLines([]) = %q, want empty", got)
	}
}

func TestSyncedBufferConcurrentWritesPreserveAllBytes(t *testing.T) {
	t.Parallel()
	var sb syncedBuffer
	var wg sync.WaitGroup
	const writers = 16
	const perWriter = 100
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if _, err := sb.Write([]byte("x")); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	got := sb.String()
	if want := writers * perWriter; len(got) != want {
		t.Errorf("len(got) = %d, want %d", len(got), want)
	}
	if strings.Trim(got, "x") != "" {
		t.Errorf("buffer contains non-x bytes: %q", got)
	}
}
