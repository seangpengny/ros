package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

func NewRedis(ctx context.Context, config RedisConfig) (*redis.Client, error) {
	if config.Addr == "" {
		return nil, errors.New("redis address is required")
	}
	client := redis.NewClient(&redis.Options{Addr: config.Addr, Password: config.Password, DB: config.DB})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
