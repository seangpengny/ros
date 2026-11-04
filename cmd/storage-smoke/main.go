package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"roslegacy/internal/storage"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	database, err := storage.NewPostgres(ctx, storage.PostgresConfig{
		URL:      requiredEnv("DATABASE_URL"),
		MinConns: int32(envInt("DATABASE_MIN_CONNS", 1)),
		MaxConns: int32(envInt("DATABASE_MAX_CONNS", 4)),
	})
	if err != nil {
		fail(err)
	}
	defer database.Close()

	cache, err := storage.NewRedis(ctx, storage.RedisConfig{
		Addr:     requiredEnv("REDIS_ADDR"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       envInt("REDIS_DB", 0),
	})
	if err != nil {
		fail(err)
	}
	defer cache.Close()

	fmt.Println("storage-smoke: PostgreSQL and Redis are reachable")
}

func requiredEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		fail(fmt.Errorf("%s is required", key))
	}
	return value
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "storage-smoke:", err)
	os.Exit(1)
}
