package matchmaking

import (
	"testing"
	"time"
)

func TestQueueMatchesFIFOByModeAndRegion(t *testing.T) {
	queue := NewQueue()
	now := time.Now().UTC()
	first, err := queue.Enqueue("one", "duo", "ap", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.Enqueue("two", "duo", "ap", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue("other-region", "duo", "eu", now); err != nil {
		t.Fatal(err)
	}
	allocation, ok := queue.Match("duo", "ap", 2)
	if !ok || len(allocation.Tickets) != 2 || allocation.Tickets[0].ID != first.ID || allocation.Tickets[1].ID != second.ID {
		t.Fatalf("unexpected allocation: %+v ok=%v", allocation, ok)
	}
	if queue.Len() != 1 {
		t.Fatalf("queue length = %d, want 1", queue.Len())
	}
}

func TestQueueCancelAndNoPartialMatch(t *testing.T) {
	queue := NewQueue()
	ticket, err := queue.Enqueue("one", "solo", "na", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !queue.Cancel(ticket.ID) || queue.Cancel(ticket.ID) {
		t.Fatal("cancel did not behave idempotently")
	}
	if _, ok := queue.Match("solo", "na", 2); ok {
		t.Fatal("partial queue produced an allocation")
	}
}

func TestQueueOwnershipGuardsCancellationAndAllocation(t *testing.T) {
	queue := NewQueue()
	now := time.Now()
	owned, err := queue.Enqueue("owner", "duo", "ap", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue("teammate", "duo", "ap", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if queue.CancelFor(owned.ID, "other") {
		t.Fatal("different account cancelled another account's ticket")
	}
	if _, ok := queue.MatchFor("other", "duo", "ap", 2); ok {
		t.Fatal("different account allocated a queue it does not own")
	}
	if !queue.CancelFor(owned.ID, "owner") {
		t.Fatal("ticket owner could not cancel its ticket")
	}
	if _, err := queue.Enqueue("other", "duo", "ap", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok := queue.MatchFor("teammate", "duo", "ap", 2); !ok {
		t.Fatal("remaining ticket owner could not allocate its queue")
	}
}

func TestQueueRejectsUnsupportedModesAndSizes(t *testing.T) {
	queue := NewQueue()
	if _, err := queue.Enqueue("owner", "trio", "ap", time.Now()); err == nil {
		t.Fatal("unsupported mode was accepted")
	}
	if _, err := queue.Enqueue("owner", "duo", "ap", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := queue.Match("duo", "ap", 1); ok {
		t.Fatal("one-player allocation was accepted")
	}
	if _, ok := queue.Match("duo", "ap", 101); ok {
		t.Fatal("allocation above 100 players was accepted")
	}
}

func TestQueueExpiresStaleTickets(t *testing.T) {
	queue := NewQueue()
	if _, err := queue.Enqueue("stale", "duo", "ap", time.Now().Add(-MaxQueueWait-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok := queue.Match("duo", "ap", 2); ok {
		t.Fatal("stale ticket produced an allocation")
	}
	if queue.Len() != 0 {
		t.Fatalf("queue length = %d, want 0 after timeout", queue.Len())
	}
}
