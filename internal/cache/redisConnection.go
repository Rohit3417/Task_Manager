package cache

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/redis/go-redis/v9"
)

func NewRedisClient(ctx context.Context) (*redis.Client, error) {
	opt, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_URL: %w", err)
	}
	client := redis.NewClient(opt)

	client.Set(ctx, "foo", "bar", 0)
	val := client.Get(ctx, "foo").Val()
	print(val)
	err1 := client.Ping(ctx).Err()
	if err1 != nil {
		return nil, fmt.Errorf("redis connection failed : %w", err)
	}

	log.Println("Connected to redis")
	return client, nil
}
