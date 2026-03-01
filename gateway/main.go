package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ObservabilityEvent represents a service-to-service call for the dashboard
type ObservabilityEvent struct {
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

// ObservabilityBus fans out events to all connected dashboard clients
type ObservabilityBus struct {
	mu      sync.RWMutex
	clients map[chan ObservabilityEvent]struct{}
}

func NewObservabilityBus() *ObservabilityBus {
	return &ObservabilityBus{
		clients: make(map[chan ObservabilityEvent]struct{}),
	}
}

func (b *ObservabilityBus) Subscribe() chan ObservabilityEvent {
	ch := make(chan ObservabilityEvent, 32)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *ObservabilityBus) Unsubscribe(ch chan ObservabilityEvent) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

func (b *ObservabilityBus) Publish(evt ObservabilityEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- evt:
		default:
			// slow client — drop rather than block
		}
	}
}

// BalanceBus fans out balance updates to subscribed SSE clients
type BalanceBus struct {
	mu      sync.RWMutex
	clients map[chan BalanceEvent]struct{}
}

type BalanceEvent struct {
	PlayerID string  `json:"playerId"`
	Balance  float64 `json:"balance"`
}

func NewBalanceBus() *BalanceBus {
	return &BalanceBus{clients: make(map[chan BalanceEvent]struct{})}
}

func (b *BalanceBus) Subscribe() chan BalanceEvent {
	ch := make(chan BalanceEvent, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *BalanceBus) Unsubscribe(ch chan BalanceEvent) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

func (b *BalanceBus) Publish(evt BalanceEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- evt:
		default:
		}
	}
}

var (
	bus        = NewObservabilityBus()
	balanceBus = NewBalanceBus()

	serviceURLs = map[string]string{
		"game-state": getEnv("GAME_STATE_URL", "https://game-state:3001"),
		"auth":       getEnv("AUTH_URL", "https://auth-service:3006"),
		"auth-ui":    getEnv("AUTH_UI_URL", "http://auth-ui-service:3010"),
		"bank":       getEnv("BANK_URL", "https://bank-service:3005"),
		"chat":       getEnv("CHAT_URL", "https://chat-service:3007"),
		"email":      getEnv("EMAIL_URL", "https://email-service:3008"),
		"document":   getEnv("DOCUMENT_URL", "http://document-service:3011"),
		"ui":         getEnv("UI_URL", "http://ui:3000"),
	}

	// mtlsTransport is used by all proxies targeting https:// upstreams.
	// Initialized in initMTLSTransport() at startup.
	mtlsTransport *http.Transport
)

func initMTLSTransport() {
	certFile := getEnv("TLS_CERT", "")
	keyFile  := getEnv("TLS_KEY", "")
	caFile   := getEnv("TLS_CA", "")

	if certFile == "" || keyFile == "" || caFile == "" {
		log.Println("[gateway][tls] no cert env vars — plain HTTP transport for upstream calls")
		return
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Fatalf("[gateway][tls] failed to load client cert: %v", err)
	}

	caCert, err := os.ReadFile(caFile)
	if err != nil {
		log.Fatalf("[gateway][tls] failed to read CA cert: %v", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		log.Fatal("[gateway][tls] failed to parse CA cert")
	}

	mtlsTransport = &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      caPool,
			MinVersion:   tls.VersionTLS12,
		},
	}
	log.Println("[gateway][tls] mTLS transport ready — upstream calls use mutual TLS")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	initMTLSTransport()

	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/health", healthHandler)

	// Observability SSE feed (no auth — dashboard is internal)
	mux.HandleFunc("/events", observabilitySSEHandler)

	// Demo control — pause/resume the demo loop
	mux.HandleFunc("/api/game/demo/pause", instrumentedProxyWithRewrite("game-state", serviceURLs["game-state"], "/api/game/demo/", "/demo/"))

	// Game routes — SSE stream and table listing are public (EventSource can't send headers)
	// Actions are open for now — will require session scope once player join flow is wired
	mux.HandleFunc("/api/game/", instrumentedProxyWithRewrite("game-state", serviceURLs["game-state"], "/api/game/", "/tables/"))

	// Auth routes → auth service (/api/auth/* → /*)
	mux.HandleFunc("/api/auth/", instrumentedProxyWithRewrite("auth", serviceURLs["auth"], "/api/auth/", "/"))

	// Email verification link
	// /verify?token=... → auth-service /verify-token?token=... (returns redirect to UI)
	mux.HandleFunc("/verify", instrumentedProxyWithRewrite("auth", serviceURLs["auth"], "/verify", "/verify-token"))

	// Passkey registration — enroll or session scope required
	// Must be registered before the general /api/auth-ui/ catch-all (longest prefix wins)
	mux.HandleFunc("/api/auth-ui/passkey/register/", requireEnrollScope(instrumentedProxyWithRewrite("auth-ui", serviceURLs["auth-ui"], "/api/auth-ui/", "/")))

	// Auth UI routes — public (login form, passkey login ceremony start)
	mux.HandleFunc("/api/auth-ui/", instrumentedProxyWithRewrite("auth-ui", serviceURLs["auth-ui"], "/api/auth-ui/", "/"))

	// Bank routes → bank service (/api/bank/* → /*) — session scope required
	mux.HandleFunc("/api/bank/export", requireSessionScope(instrumentedProxyWithRewrite("bank", serviceURLs["bank"], "/api/bank/export", "/export")))
	mux.HandleFunc("/api/bank/", requireSessionScope(instrumentedProxyWithRewrite("bank", serviceURLs["bank"], "/api/bank/", "/")))

	// Chat routes → chat service (/api/chat/* → /*)
	mux.HandleFunc("/api/chat/", instrumentedProxyWithRewrite("chat", serviceURLs["chat"], "/api/chat/", "/"))

	// Email routes → email service (/api/email/* → /*)
	mux.HandleFunc("/api/email/", instrumentedProxyWithRewrite("email", serviceURLs["email"], "/api/email/", "/"))

	// DEV ONLY — wipe all state and re-seed
	mux.HandleFunc("/dev/reset", devResetHandler)
	mux.HandleFunc("/dev/demo-token", instrumentedProxyWithRewrite("auth", serviceURLs["auth"], "/dev/demo-token", "/dev/demo-token"))
	mux.HandleFunc("/api/bank/balance/stream", balanceSSEHandler)

	// UI catch-all — must be last. Proxies everything else to the UI container.
	// In production this would be a CDN or static file server.
	mux.HandleFunc("/", instrumentedProxy("ui", serviceURLs["ui"]))

	port      := getEnv("PORT", "8021")
	certFile  := getEnv("TLS_CERT", "")
	keyFile   := getEnv("TLS_KEY", "")

	// Subscribe to Redis for internal service events
	go subscribeRedis()
	go func() {
		// Reuse the same Redis connection strategy — wait for redis to be ready
		redisAddr := getEnv("REDIS_URL", "redis:6379")
		var rdb *redis.Client
		for i := 0; i < 10; i++ {
			rdb = redis.NewClient(&redis.Options{Addr: redisAddr})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := rdb.Ping(ctx).Err()
			cancel()
			if err == nil {
				break
			}
			rdb.Close()
			rdb = nil
			time.Sleep(2 * time.Second)
		}
		if rdb != nil {
			defer rdb.Close()
			subscribeRedisBalance(rdb)
		}
	}()

	// Gateway listens externally on plain HTTP — TLS termination happens here
	// for external clients. Internal upstreams use mTLS via mtlsTransport.
	// (Gateway could also terminate TLS externally with a proper cert — future work)
	_ = certFile
	_ = keyFile
	log.Printf("[gateway] starting on :%s (plain HTTP external, mTLS upstream)", port)
	if err := http.ListenAndServe(":"+port, corsMiddleware(mux)); err != nil {
		log.Fatal(err)
	}
}

// instrumentedProxyWithRewrite proxies with prefix rewriting e.g. /api/game/ → /tables/
func instrumentedProxyWithRewrite(callee, targetURL, stripPrefix, addPrefix string) http.HandlerFunc {
	target, err := url.Parse(targetURL)
	if err != nil {
		log.Fatalf("invalid upstream URL for %s: %v", callee, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // flush immediately — required for SSE pass-through
	if target.Scheme == "https" && mtlsTransport != nil {
		proxy.Transport = mtlsTransport
	}
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		// Rewrite path: strip incoming prefix, add upstream prefix
		path := strings.TrimPrefix(req.URL.Path, stripPrefix)
		req.URL.Path = addPrefix + path
		if req.URL.RawPath != "" {
			rawPath := strings.TrimPrefix(req.URL.RawPath, stripPrefix)
			req.URL.RawPath = addPrefix + rawPath
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error [%s]: %v", callee, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"code":    "upstream_error",
			"message": fmt.Sprintf("%s service unavailable", callee),
		})
	}

	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		isSSE := r.Header.Get("Accept") == "text/event-stream"
		rw := &statusRecorder{ResponseWriter: w, status: 200}
		proxy.ServeHTTP(rw, r)
		latency := time.Since(start).Milliseconds()
		bus.Publish(ObservabilityEvent{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Caller:    "gateway",
			Callee:    callee,
			Method:    r.Method,
			Path:      r.URL.Path,
			Protocol:  protocolFor(isSSE, r),
			StatusCode: rw.status,
			LatencyMs:  latency,
		})
		log.Printf("[gateway→%s] %s %s %d (%dms)", callee, r.Method, r.URL.Path, rw.status, latency)
	}
}

// instrumentedProxy creates a reverse proxy that publishes observability events
func instrumentedProxy(callee, targetURL string) http.HandlerFunc {
	target, err := url.Parse(targetURL)
	if err != nil {
		log.Fatalf("invalid upstream URL for %s: %v", callee, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // flush immediately — required for SSE pass-through
	if target.Scheme == "https" && mtlsTransport != nil {
		proxy.Transport = mtlsTransport
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error [%s]: %v", callee, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"code":    "upstream_error",
			"message": fmt.Sprintf("%s service unavailable", callee),
		})
	}

	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Detect SSE requests — don't buffer them
		isSSE := r.Header.Get("Accept") == "text/event-stream"

		// Publish request event
		reqEvt := ObservabilityEvent{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Caller:    "gateway",
			Callee:    callee,
			Method:    r.Method,
			Path:      r.URL.Path,
			Protocol:  protocolFor(isSSE, r),
		}

		// Track response status
		rw := &statusRecorder{ResponseWriter: w, status: 200}
		proxy.ServeHTTP(rw, r)

		latency := time.Since(start).Milliseconds()
		reqEvt.StatusCode = rw.status
		reqEvt.LatencyMs = latency
		bus.Publish(reqEvt)

		log.Printf("[gateway→%s] %s %s %d (%dms)", callee, r.Method, r.URL.Path, rw.status, latency)
	}
}

func protocolFor(isSSE bool, r *http.Request) string {
	if isSSE {
		return "sse"
	}
	if r.Header.Get("Upgrade") == "websocket" {
		return "websocket"
	}
	return "http"
}

// extractJWTClaims decodes JWT payload without signature verification.
// Scope decisions are routing-only — auth-service still fully verifies on every call.
func extractJWTClaims(r *http.Request) map[string]interface{} {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(auth, "Bearer "), ".")
	if len(parts) != 3 {
		return nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil
	}
	return claims
}

func scopeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}

// requireSessionScope enforces scope: "session" on protected routes.
// 401 = no token. 403 = wrong token type (bootstrap token used on game route).
func requireSessionScope(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := extractJWTClaims(r)
		if claims == nil {
			scopeError(w, http.StatusUnauthorized, "auth_required", "authentication required")
			return
		}
		scope, _ := claims["scope"].(string)
		if scope != "session" {
			// 403 not 401 — they have a token, it's just the wrong type
			scopeError(w, http.StatusForbidden, "wrong_token_scope",
				"session token required — complete passkey enrollment first")
			return
		}
		// Inject player ID for downstream services
		if sub, ok := claims["sub"].(string); ok {
			r.Header.Set("X-Player-ID", sub)
		}
		next(w, r)
	}
}

// requireEnrollScope accepts enroll or session scope — used on passkey registration endpoints.
func requireEnrollScope(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := extractJWTClaims(r)
		if claims == nil {
			scopeError(w, http.StatusUnauthorized, "auth_required", "authentication required")
			return
		}
		scope, _ := claims["scope"].(string)
		if scope != "enroll" && scope != "session" {
			scopeError(w, http.StatusForbidden, "wrong_token_scope", "enroll or session token required")
			return
		}
		if sub, ok := claims["sub"].(string); ok {
			r.Header.Set("X-Player-ID", sub)
		}
		next(w, r)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func observabilitySSEHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	// Send connected event
	fmt.Fprintf(w, "event: connected\ndata: {\"service\":\"gateway\"}\n\n")
	flusher.Flush()

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "event: service_call\ndata: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// balanceSSEHandler streams balance updates to the UI.
// No auth — demo player is public; production would scope per JWT.
func balanceSSEHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := balanceBus.Subscribe()
	defer balanceBus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "event: balance_update\ndata: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// subscribeRedisBalance subscribes to tca:balance and fans out to balanceBus.
func subscribeRedisBalance(rdb *redis.Client) {
	sub := rdb.Subscribe(context.Background(), "tca:balance")
	defer sub.Close()
	log.Printf("[gateway] subscribed to Redis channel tca:balance")
	for msg := range sub.Channel() {
		var evt BalanceEvent
		if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
			log.Printf("[gateway] balance event parse error: %v", err)
			continue
		}
		balanceBus.Publish(evt)
	}
}

// subscribeRedis subscribes to the observability Redis channel and feeds
// events into the local bus so SSE clients see internal service calls.
func subscribeRedis() {
	redisAddr := getEnv("REDIS_URL", "redis:6379")

	// Retry until Redis is ready
	var rdb *redis.Client
	for i := 0; i < 10; i++ {
		rdb = redis.NewClient(&redis.Options{Addr: redisAddr})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			log.Printf("[gateway] Redis connected at %s", redisAddr)
			break
		}
		log.Printf("[gateway] Redis not ready (%d/10), retrying...", i+1)
		rdb.Close()
		rdb = nil
		time.Sleep(2 * time.Second)
	}
	if rdb == nil {
		log.Printf("[gateway] Redis unavailable — internal events will not appear on dashboard")
		return
	}
	defer rdb.Close()

	sub := rdb.Subscribe(context.Background(), "tca:events")
	defer sub.Close()

	log.Printf("[gateway] subscribed to Redis channel tca:events")

	ch := sub.Channel()
	for msg := range ch {
		var evt ObservabilityEvent
		if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
			log.Printf("[gateway] redis event parse error: %v", err)
			continue
		}
		bus.Publish(evt)
	}
}

// devResetHandler fans out POST /dev/reset to auth-service and bank-service.
// DEV ONLY — gate this off before production.
func devResetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	type result struct {
		service string
		ok      bool
		msg     string
	}

	services := map[string]string{
		"auth-service": serviceURLs["auth"] + "/dev/reset",
		"bank-service": serviceURLs["bank"] + "/dev/reset",
	}

	results := make(map[string]string)
	var client *http.Client
	if mtlsTransport != nil {
		client = &http.Client{Timeout: 5 * time.Second, Transport: mtlsTransport}
	} else {
		client = &http.Client{Timeout: 5 * time.Second}
	}

	for name, url := range services {
		resp, err := client.Post(url, "application/json", nil)
		if err != nil {
			results[name] = "error: " + err.Error()
			log.Printf("[gateway] dev/reset: %s failed: %v", name, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			results[name] = "ok"
		} else {
			results[name] = fmt.Sprintf("error: status %d", resp.StatusCode)
		}
	}

	log.Printf("[gateway] DEV RESET executed: %v", results)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"reset":   true,
		"results": results,
	})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	upstreams := make(map[string]string)
	for name, svcURL := range serviceURLs {
		status := checkUpstream(svcURL + "/health")
		upstreams[name] = status
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "healthy",
		"service":  "gateway",
		"version":  "0.1.0",
		"upstream": upstreams,
	})
}

func checkUpstream(healthURL string) string {
	var client *http.Client
	if mtlsTransport != nil && strings.HasPrefix(healthURL, "https://") {
		client = &http.Client{Timeout: 2 * time.Second, Transport: mtlsTransport}
	} else {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	resp, err := client.Get(healthURL)
	if err != nil {
		return "unreachable"
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)
	if resp.StatusCode == 200 {
		return "healthy"
	}
	return fmt.Sprintf("degraded (%d)", resp.StatusCode)
}

// statusRecorder wraps ResponseWriter to capture status code
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Flush implements http.Flusher — required for SSE pass-through via reverse proxy.
// Without this, FlushInterval: -1 on the proxy has no effect.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
