// Observability Service
// =====================
// Language : Go
// Container: Scratch (static binary, no shell, no attack surface)
//
// Receives event reports from all services, filters sensitive data,
// publishes to Redis pub/sub channel "tca:events".
// Gateway subscribes to Redis and fans out to SSE clients.
//
// No service needs to know Redis exists.
// No service needs to know who consumes events.
// They POST here and forget.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// ── Event shapes ──────────────────────────────────────────────────────────────

// InboundEvent is what services POST to us (snake_case, minimal fields)
type InboundEvent struct {
	Caller     string `json:"caller"`
	Callee     string `json:"callee"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	StatusCode int    `json:"status_code"`
	LatencyMs  int64  `json:"latency_ms"`
	Protocol   string `json:"protocol"`
}

// PublishedEvent is what we put on Redis (camelCase, matches frontend contract)
type PublishedEvent struct {
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	Caller     string `json:"caller"`
	Callee     string `json:"callee"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	StatusCode int    `json:"statusCode"`
	LatencyMs  int64  `json:"latencyMs"`
	Protocol   string `json:"protocol"`
}

// ── Allowlists ────────────────────────────────────────────────────────────────

var knownServices = map[string]bool{
	"gateway":               true,
	"game-state":            true,
	"deck-service":          true,
	"hand-evaluator":        true,
	"dealer-ai":             true,
	"bank-service":          true,
	"auth-service":          true,
	"chat-service":          true,
	"email-service":         true,
	"observability-service": true,
}

var knownMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true,
	"DELETE": true, "PATCH": true, "HEAD": true,
}

var knownProtocols = map[string]bool{
	"http": true, "sse": true, "websocket": true, "mtls": true,
}

// ── Sanitization patterns ─────────────────────────────────────────────────────

var (
	reIPv4  = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	reIPv6  = regexp.MustCompile(`([0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}`)
	reUUID  = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reJWT   = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	reQuery = regexp.MustCompile(`([?&][^=&]+)=[^&]*`)
)

func sanitizePath(path string) string {
	path = reJWT.ReplaceAllString(path, "[token]")
	path = reIPv4.ReplaceAllString(path, "[ip]")
	path = reIPv6.ReplaceAllString(path, "[ip]")
	path = reUUID.ReplaceAllString(path, "[id]")
	path = reQuery.ReplaceAllString(path, "$1=[redacted]")
	return path
}

// ── Counters ──────────────────────────────────────────────────────────────────

var (
	eventsReceived  atomic.Int64
	eventsPublished atomic.Int64
	eventsDropped   atomic.Int64
)

// ── Redis ─────────────────────────────────────────────────────────────────────

const redisChannel = "tca:events"

var rdb *redis.Client

func initRedis(addr string) error {
	rdb = redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return rdb.Ping(ctx).Err()
}

func publish(evt PublishedEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		log.Printf("marshal error: %v", err)
		eventsDropped.Add(1)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Publish(ctx, redisChannel, data).Err(); err != nil {
		log.Printf("redis publish error: %v", err)
		eventsDropped.Add(1)
		return
	}
	eventsPublished.Add(1)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func eventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var inbound InboundEvent
	if err := json.NewDecoder(r.Body).Decode(&inbound); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	eventsReceived.Add(1)

	// ── Validate allowlists ───────────────────────────────────────────────────
	if !knownServices[inbound.Caller] || !knownServices[inbound.Callee] {
		log.Printf("DROP unknown service: caller=%q callee=%q", inbound.Caller, inbound.Callee)
		eventsDropped.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !knownMethods[strings.ToUpper(inbound.Method)] {
		log.Printf("DROP unknown method: %q", inbound.Method)
		eventsDropped.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !knownProtocols[strings.ToLower(inbound.Protocol)] {
		log.Printf("DROP unknown protocol: %q", inbound.Protocol)
		eventsDropped.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if inbound.StatusCode < 100 || inbound.StatusCode > 599 {
		log.Printf("DROP invalid status code: %d", inbound.StatusCode)
		eventsDropped.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if inbound.LatencyMs < 0 {
		inbound.LatencyMs = 0
	}

	// ── Sanitize ──────────────────────────────────────────────────────────────
	cleaned := PublishedEvent{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Caller:     inbound.Caller,
		Callee:     inbound.Callee,
		Method:     strings.ToUpper(inbound.Method),
		Path:       sanitizePath(inbound.Path),
		StatusCode: inbound.StatusCode,
		LatencyMs:  inbound.LatencyMs,
		Protocol:   strings.ToLower(inbound.Protocol),
	}

	// Publish non-blocking
	go publish(cleaned)

	w.WriteHeader(http.StatusAccepted)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	redisStatus := "connected"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		redisStatus = "disconnected"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":            "healthy",
		"service":           "observability-service",
		"language":          "Go",
		"container":         "scratch",
		"redis":             redisStatus,
		"redis_channel":     redisChannel,
		"events_received":   eventsReceived.Load(),
		"events_published":  eventsPublished.Load(),
		"events_dropped":    eventsDropped.Load(),
	})
}

func rulesHandler(w http.ResponseWriter, r *http.Request) {
	services := make([]string, 0, len(knownServices))
	for s := range knownServices {
		services = append(services, s)
	}
	methods := make([]string, 0, len(knownMethods))
	for m := range knownMethods {
		methods = append(methods, m)
	}
	protocols := make([]string, 0, len(knownProtocols))
	for p := range knownProtocols {
		protocols = append(protocols, p)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"service_allowlist":  services,
		"method_allowlist":   methods,
		"protocol_allowlist": protocols,
		"path_sanitization": []map[string]string{
			{"pattern": "JWT tokens",     "replacement": "[token]"},
			{"pattern": "IPv4 addresses", "replacement": "[ip]"},
			{"pattern": "IPv6 addresses", "replacement": "[ip]"},
			{"pattern": "UUIDs",          "replacement": "[id]"},
			{"pattern": "query values",   "replacement": "[redacted]"},
		},
	})
}

// ── Main ──────────────────────────────────────────────────────────────────────

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	redisAddr := getEnv("REDIS_URL", "redis:6379")
	port := getEnv("PORT", "3009")

	log.Printf("[observability-service] starting on :%s", port)
	log.Printf("[observability-service] connecting to Redis at %s", redisAddr)

	// Retry Redis connection — it may still be starting
	for i := 0; i < 10; i++ {
		if err := initRedis(redisAddr); err != nil {
			log.Printf("[observability-service] Redis not ready, retrying (%d/10): %v", i+1, err)
			time.Sleep(2 * time.Second)
		} else {
			log.Printf("[observability-service] Redis connected")
			break
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/event", eventHandler)
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/rules", rulesHandler)

	log.Printf("[observability-service] ready — publishing to Redis channel %q", redisChannel)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
