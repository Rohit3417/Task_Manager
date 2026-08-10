package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"workspace_project/graph"
	"workspace_project/internal/auth"
	"workspace_project/internal/cache"
	"workspace_project/internal/db"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"github.com/joho/godotenv"
	"github.com/vektah/gqlparser/v2/ast"
)

const defaultPort = "8080"

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")

		if header != "" {
			if !strings.HasPrefix(header, "Bearer ") {
				writeError(w, http.StatusUnauthorized, "unauthorized access")
				return
			}
			token := strings.TrimPrefix(header, "Bearer ")
			claims, err := auth.ValidateToken(token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			mapClaims, ok := claims.(jwt.MapClaims)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			userID, ok := mapClaims["user_id"].(string)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			ctx := context.WithValue(r.Context(), "userID", userID)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func RateLimitMiddleware(client *redis.Client, limit int, windowSeconds int) func(http.Handler) http.Handler {
	//KEYS[1] is the key name (ratelimit:user_abc123)
	//ARGV[1] is the window length in seconds
	//INCR on a key that doesn't exist creates it at 1 — that's how you detect "this is the first request in a new window": current == 1
	script := redis.NewScript(`		
				local current = redis.call("INCR", KEYS[1])		
				if current == 1 then
				     redis.call("EXPIRE", KEYS[1], ARGV[1])
				end
				return current
			`)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identifier, ok := r.Context().Value("userID").(string)
			if !ok || identifier == "" {
				host, _, err := net.SplitHostPort(r.RemoteAddr)
				if err != nil {
					host = r.RemoteAddr
				}
				identifier = host
			}

			key := "ratelimit:" + identifier

			count, err := script.Run(r.Context(), client, []string{key}, windowSeconds).Int()
			if err != nil {
				log.Printf("rate limit check failed for %s: %v", identifier, err)
				next.ServeHTTP(w, r)
				return
			}
			if count > limit {
				//reading the ttl for retry - after sometime
				ttl, ttlErr := client.TTL(r.Context(), key).Result()
				if ttlErr == nil {
					w.Header().Set("Retry-After", strconv.Itoa(int(ttl.Seconds())))
				}
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded, try again later")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error":     msg,
		"code":      status,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("no .env file found, relying on environment variables")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	limit, err := strconv.Atoi(os.Getenv("RATE_LIMITING"))
	if err != nil {
		limit = 100 //100 requests/min
	}
	windowSeconds, err := strconv.Atoi(os.Getenv("RATE_WINDOW"))
	if err != nil {
		windowSeconds = 60 //60 seconds window
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx)
	if err != nil {
		log.Fatalf("%v", err)
		return
	}
	defer pool.Close()

	client, err := cache.NewRedisClient(ctx)
	if err != nil {
		log.Fatalf("%v", err)
		return
	}
	defer client.Close()

	limiter := RateLimitMiddleware(client, limit, windowSeconds)

	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{Pool: pool, Client: client}}))

	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})

	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))
	loggedHandler := middleware.Logger(srv)
	srv.Use(extension.Introspection{})
	srv.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](100),
	})

	http.Handle("/", playground.Handler("GraphQL playground", "/query"))
	http.Handle("/query", withCORS(AuthMiddleware(limiter(loggedHandler))))

	log.Printf("connect to http://localhost:%s/ for GraphQL playground", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
