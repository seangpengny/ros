package matchmaking

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Ticket struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Mode      string    `json:"mode"`
	Region    string    `json:"region"`
	QueuedAt  time.Time `json:"queued_at"`
	Status    string    `json:"status,omitempty"`
}

type Allocation struct {
	ID      string   `json:"id"`
	Mode    string   `json:"mode"`
	Region  string   `json:"region"`
	Tickets []Ticket `json:"tickets"`
}

type Queue struct {
	mu     sync.Mutex
	nextID uint64
	items  []Ticket
}

const MaxQueueWait = 5 * time.Minute

func NewQueue() *Queue { return &Queue{} }

func (q *Queue) Enqueue(accountID, mode, region string, now time.Time) (Ticket, error) {
	if accountID == "" || mode == "" || region == "" {
		return Ticket{}, errors.New("account, mode, and region are required")
	}
	if err := ValidateMode(mode); err != nil {
		return Ticket{}, err
	}
	if err := ValidateRegion(region); err != nil {
		return Ticket{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(now.UTC())
	for _, item := range q.items {
		if item.AccountID == accountID && item.Mode == mode && item.Region == region {
			return item, nil
		}
	}
	q.nextID++
	ticket := Ticket{ID: fmt.Sprintf("ticket-%08d", q.nextID), AccountID: accountID, Mode: mode, Region: region, QueuedAt: now, Status: "queued"}
	q.items = append(q.items, ticket)
	return ticket, nil
}

func (q *Queue) Cancel(ticketID string) bool {
	return q.cancel(ticketID, "")
}

func (q *Queue) CancelFor(ticketID, accountID string) bool {
	return q.cancel(ticketID, accountID)
}

func (q *Queue) cancel(ticketID, accountID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(time.Now().UTC())
	for index, item := range q.items {
		if item.ID == ticketID && (accountID == "" || item.AccountID == accountID) {
			item.Status = "cancelled"
			q.items = append(q.items[:index], q.items[index+1:]...)
			return true
		}
	}
	return false
}

func (q *Queue) Match(mode, region string, size int) (Allocation, bool) {
	return q.matchFor("", mode, region, size)
}

func (q *Queue) MatchFor(accountID, mode, region string, size int) (Allocation, bool) {
	return q.matchFor(accountID, mode, region, size)
}

func (q *Queue) matchFor(accountID, mode, region string, size int) (Allocation, bool) {
	if size < 2 || size > 100 || ValidateMode(mode) != nil || ValidateRegion(region) != nil {
		return Allocation{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(time.Now().UTC())
	if accountID != "" {
		owned := false
		for _, item := range q.items {
			if item.AccountID == accountID && item.Mode == mode && item.Region == region && item.Status == "queued" {
				owned = true
				break
			}
		}
		if !owned {
			return Allocation{}, false
		}
	}
	selected := make([]Ticket, 0, size)
	remaining := make([]Ticket, 0, len(q.items))
	for _, item := range q.items {
		if len(selected) < size && item.Mode == mode && item.Region == region {
			selected = append(selected, item)
		} else {
			remaining = append(remaining, item)
		}
	}
	if len(selected) != size {
		return Allocation{}, false
	}
	q.items = remaining
	q.nextID++
	for index := range selected {
		selected[index].Status = "matched"
	}
	return Allocation{ID: fmt.Sprintf("allocation-%08d", q.nextID), Mode: mode, Region: region, Tickets: selected}, true
}

func ValidateMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "solo", "duo", "squad":
		return nil
	default:
		return errors.New("mode must be solo, duo, or squad")
	}
}

func ValidateRegion(region string) error {
	region = strings.TrimSpace(region)
	if region == "" || len(region) > 32 || strings.ContainsAny(region, " :/\\{}[]\"'") {
		return errors.New("region is required and bounded")
	}
	return nil
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(time.Now().UTC())
	return len(q.items)
}

func (q *Queue) expireLocked(now time.Time) {
	active := q.items[:0]
	for _, item := range q.items {
		if !item.QueuedAt.IsZero() && now.Sub(item.QueuedAt) > MaxQueueWait {
			continue
		}
		active = append(active, item)
	}
	q.items = active
}

var _ interface {
	Enqueue(string, string, string, time.Time) (Ticket, error)
	Cancel(string) bool
	CancelFor(string, string) bool
	Match(string, string, int) (Allocation, bool)
	MatchFor(string, string, string, int) (Allocation, bool)
	Len() int
} = (*Queue)(nil)
