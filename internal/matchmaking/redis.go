package matchmaking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisQueueTTL = 24 * time.Hour

// RedisQueue is the production API queue implementation. Queue entries are
// ephemeral, while match allocation records are expected to be persisted by
// the allocation worker before a dedicated server is admitted.
type RedisQueue struct {
	client  *redis.Client
	timeout time.Duration
}

func NewRedisQueue(client *redis.Client) (*RedisQueue, error) {
	if client == nil {
		return nil, errors.New("redis client is required")
	}
	return &RedisQueue{client: client, timeout: 3 * time.Second}, nil
}

func (q *RedisQueue) Enqueue(accountID, mode, region string, now time.Time) (Ticket, error) {
	if err := validateQueueField(accountID, "account"); err != nil {
		return Ticket{}, err
	}
	if err := validateQueueField(mode, "mode"); err != nil {
		return Ticket{}, err
	}
	if err := ValidateMode(mode); err != nil {
		return Ticket{}, err
	}
	if err := ValidateRegion(region); err != nil {
		return Ticket{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	defer cancel()
	indexKey := queueAccountKey(accountID, mode, region)
	if existing, err := q.client.Get(ctx, indexKey).Result(); err == nil {
		if ticket, readErr := q.readTicket(ctx, existing); readErr == nil && ticket.Status == "queued" {
			if !ticketExpired(ticket, now.UTC()) {
				return ticket, nil
			}
			ticket.Status = "expired"
			_ = q.writeTicket(ctx, ticket)
			_ = q.client.Del(ctx, indexKey).Err()
		}
	} else if !errors.Is(err, redis.Nil) {
		return Ticket{}, fmt.Errorf("read existing ticket: %w", err)
	}
	sequence, err := q.client.Incr(ctx, "ros:matchmaking:next").Result()
	if err != nil {
		return Ticket{}, fmt.Errorf("allocate ticket id: %w", err)
	}
	ticket := Ticket{ID: fmt.Sprintf("ticket-%08d", sequence), AccountID: accountID, Mode: mode, Region: region, QueuedAt: now.UTC(), Status: "queued"}
	if err := q.writeTicket(ctx, ticket); err != nil {
		return Ticket{}, err
	}
	pipe := q.client.TxPipeline()
	pipe.Set(ctx, indexKey, ticket.ID, redisQueueTTL)
	pipe.RPush(ctx, queueKey(mode, region), ticket.ID)
	if _, err := pipe.Exec(ctx); err != nil {
		return Ticket{}, fmt.Errorf("enqueue ticket: %w", err)
	}
	return ticket, nil
}

func (q *RedisQueue) Cancel(ticketID string) bool {
	return q.cancel(ticketID, "")
}

func (q *RedisQueue) CancelFor(ticketID, accountID string) bool {
	return q.cancel(ticketID, accountID)
}

func (q *RedisQueue) cancel(ticketID, accountID string) bool {
	if ticketID == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	defer cancel()
	ticket, err := q.readTicket(ctx, ticketID)
	if err != nil || ticket.Status != "queued" || (accountID != "" && ticket.AccountID != accountID) {
		return false
	}
	ticket.Status = "cancelled"
	if err := q.writeTicket(ctx, ticket); err != nil {
		return false
	}
	_ = q.client.Del(ctx, queueAccountKey(ticket.AccountID, ticket.Mode, ticket.Region)).Err()
	return true
}

func (q *RedisQueue) Match(mode, region string, size int) (Allocation, bool) {
	return q.matchFor("", mode, region, size)
}

func (q *RedisQueue) MatchFor(accountID, mode, region string, size int) (Allocation, bool) {
	return q.matchFor(accountID, mode, region, size)
}

func (q *RedisQueue) matchFor(accountID, mode, region string, size int) (Allocation, bool) {
	if size < 2 || size > 100 || ValidateMode(mode) != nil || ValidateRegion(region) != nil {
		return Allocation{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	defer cancel()
	lockKey := queueLockKey(mode, region)
	locked, err := q.client.SetNX(ctx, lockKey, "1", 5*time.Second).Result()
	if err != nil || !locked {
		return Allocation{}, false
	}
	defer q.client.Del(ctx, lockKey)
	if accountID != "" {
		ownedTicketID, ownerErr := q.client.Get(ctx, queueAccountKey(accountID, mode, region)).Result()
		if ownerErr != nil {
			return Allocation{}, false
		}
		ownedTicket, readErr := q.readTicket(ctx, ownedTicketID)
		if readErr != nil || ownedTicket.Status != "queued" || ownedTicket.AccountID != accountID {
			return Allocation{}, false
		}
	}
	selected := make([]Ticket, 0, size)
	for len(selected) < size {
		id, popErr := q.client.LPop(ctx, queueKey(mode, region)).Result()
		if errors.Is(popErr, redis.Nil) {
			break
		}
		if popErr != nil {
			break
		}
		ticket, readErr := q.readTicket(ctx, id)
		if readErr != nil || ticket.Status != "queued" {
			continue
		}
		if ticketExpired(ticket, time.Now().UTC()) {
			ticket.Status = "expired"
			_ = q.writeTicket(ctx, ticket)
			_ = q.client.Del(ctx, queueAccountKey(ticket.AccountID, ticket.Mode, ticket.Region)).Err()
			continue
		}
		selected = append(selected, ticket)
	}
	if len(selected) != size {
		for index := len(selected) - 1; index >= 0; index-- {
			_ = q.client.LPush(ctx, queueKey(mode, region), selected[index].ID).Err()
		}
		return Allocation{}, false
	}
	for index := range selected {
		selected[index].Status = "matched"
		_ = q.writeTicket(ctx, selected[index])
		_ = q.client.Del(ctx, queueAccountKey(selected[index].AccountID, mode, region)).Err()
	}
	sequence, err := q.client.Incr(ctx, "ros:matchmaking:allocation").Result()
	if err != nil {
		return Allocation{}, false
	}
	return Allocation{ID: fmt.Sprintf("allocation-%08d", sequence), Mode: mode, Region: region, Tickets: selected}, true
}

func (q *RedisQueue) Len() int {
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	defer cancel()
	keys, err := q.client.Keys(ctx, "ros:matchmaking:queue:*").Result()
	if err != nil {
		return 0
	}
	length := int64(0)
	for _, key := range keys {
		value, err := q.client.LLen(ctx, key).Result()
		if err == nil {
			length += value
		}
	}
	return int(length)
}

func (q *RedisQueue) readTicket(ctx context.Context, id string) (Ticket, error) {
	raw, err := q.client.Get(ctx, ticketKey(id)).Bytes()
	if err != nil {
		return Ticket{}, err
	}
	var ticket Ticket
	if err := json.Unmarshal(raw, &ticket); err != nil {
		return Ticket{}, err
	}
	return ticket, nil
}

func (q *RedisQueue) writeTicket(ctx context.Context, ticket Ticket) error {
	raw, err := json.Marshal(ticket)
	if err != nil {
		return err
	}
	return q.client.Set(ctx, ticketKey(ticket.ID), raw, redisQueueTTL).Err()
}

func ticketExpired(ticket Ticket, now time.Time) bool {
	return !ticket.QueuedAt.IsZero() && now.Sub(ticket.QueuedAt) > MaxQueueWait
}

func ticketKey(id string) string { return "ros:matchmaking:ticket:" + id }

func queueKey(mode, region string) string { return "ros:matchmaking:queue:" + mode + ":" + region }

func queueAccountKey(accountID, mode, region string) string {
	return "ros:matchmaking:account:" + accountID + ":" + mode + ":" + region
}

func queueLockKey(mode, region string) string { return "ros:matchmaking:lock:" + mode + ":" + region }

func validateQueueField(value, name string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || strings.ContainsAny(value, ":{}[]\\\"'") {
		return fmt.Errorf("%s is required and bounded", name)
	}
	return nil
}

var _ interface {
	Enqueue(string, string, string, time.Time) (Ticket, error)
	Cancel(string) bool
	CancelFor(string, string) bool
	Match(string, string, int) (Allocation, bool)
	MatchFor(string, string, string, int) (Allocation, bool)
	Len() int
} = (*RedisQueue)(nil)
