package ws

import (
	"context"
	"encoding/json"
	"log"
	"workspace_project/graph/model"

	"github.com/redis/go-redis/v9"
)

type BoardEvent struct {
	Type    string      `json:"type"`
	BoardID string      `json:"boardId"`
	Task    *model.Task `json:"task"`
}

type Client struct {
	BoardID string
	Send    chan BoardEvent
}

type Hub struct {
	clients    map[string]map[*Client]bool
	register   chan *Client
	unregister chan *Client
	BroadCast  chan BoardEvent
}

func (h *Hub) Register(c *Client) {
	h.register <- c
}

func (h *Hub) Unregister(c *Client) {
	h.unregister <- c
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[string]map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		BroadCast:  make(chan BoardEvent),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			if h.clients[c.BoardID] == nil {
				h.clients[c.BoardID] = make(map[*Client]bool)
			}
			h.clients[c.BoardID][c] = true

		case c := <-h.unregister:
			if _, ok := h.clients[c.BoardID][c]; ok {
				delete(h.clients[c.BoardID], c)
				close(c.Send)
			}

		case event := <-h.BroadCast:
			for c := range h.clients[event.BoardID] {
				select {
				case c.Send <- event:
				default:
				}
			}
		}
	}
}

func StartRedisSubscriber(ctx context.Context, rdb *redis.Client, hub *Hub) {
	pubsub := rdb.PSubscribe(ctx, "board:*")
	defer pubsub.Close()

	if _, err := pubsub.Receive(ctx); err != nil {
		log.Printf("redis subscribe failed: %v", err)
		return
	}

	ch := pubsub.Channel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var event BoardEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				log.Printf("failed to unmarshal board event: %v", err)
				continue
			}
			hub.BroadCast <- event
		case <-ctx.Done():
			return
		}
	}
}
