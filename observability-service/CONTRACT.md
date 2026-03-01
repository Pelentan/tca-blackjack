# Observability Service — Contract

**Last Updated:** 2026-02-24  
**Language:** Go  
**Deployment:** Scratch container, static binary  
**Status:** Design / Pre-implementation

---

## Purpose

Receives structured event reports from all services, filters sensitive data,
and publishes to Redis pub/sub. The gateway subscribes to Redis and fans
events out to connected SSE clients (the observability dashboard).

No service needs to know Redis exists. No service needs to know who is
consuming events. They just POST here and forget.

```
[any service] → POST /event → [observability-service] → Redis pub/sub → [gateway] → SSE → [browser]
```

---

## Design Principles

- **Fire and forget** — callers do not wait for a response beyond 202.
  Observability is never on the critical path.
- **Filter at ingress** — sensitive data is stripped here, once,
  before anything hits Redis or the browser.
- **Owns Redis** — no other service publishes to the observability channel.
  Redis is an implementation detail of this service.
- **Scratch container** — static Go binary, no shell, no package manager,
  no attack surface.

---

## Endpoint

### POST /event

Report a service-to-service call. Fire and forget.

**Request:**
```json
{
  "caller": "string",        // Service name e.g. "game-state"
  "callee": "string",        // Service name e.g. "deck-service"
  "method": "string",        // HTTP method: GET POST PUT DELETE
  "path": "string",          // Request path e.g. "/deal"
  "status_code": 200,        // HTTP response status
  "latency_ms": 12,          // Round-trip latency in milliseconds
  "protocol": "string"       // "http" | "sse" | "websocket" | "mtls"
}
```

**Response:**
```
HTTP 202 Accepted
```
Always 202. Callers do not need to inspect the response.
If the service is unavailable, callers log and move on.

**Notes:**
- `id` and `timestamp` are assigned by this service, not the caller
- Caller does not need to generate UUIDs or timestamps
- Path is sanitized before publishing (see filtering rules below)

---

## Filtering Rules

Applied in order before publishing to Redis. Rules are additive —
a field that matches any rule is scrubbed.

### Path Sanitization
Patterns stripped or replaced in the `path` field:

| Pattern | Replacement | Reason |
|---------|-------------|--------|
| IPv4 addresses | `[ip]` | No IPs on dashboard |
| IPv6 addresses | `[ip]` | No IPs on dashboard |
| JWT tokens (Bearer ...) | `[token]` | Credential leak |
| UUIDs | `[id]` | Reduce noise, prevent user ID exposure |
| Query string values | key names only | Values may contain sensitive data |

### Field Rules
| Field | Rule |
|-------|------|
| `caller` / `callee` | Allowlist only — must match known service names |
| `method` | Allowlist: GET POST PUT DELETE PATCH HEAD |
| `protocol` | Allowlist: http sse websocket mtls |
| `status_code` | Must be valid HTTP status (100-599) |
| `latency_ms` | Must be non-negative integer |

### Known Service Allowlist
```
gateway, game-state, deck-service, hand-evaluator,
dealer-ai, bank-service, auth-service, chat-service,
email-service, observability-service
```

Any event with an unknown caller or callee is **dropped silently**.
This prevents a compromised service from injecting arbitrary names
into the dashboard.

---

## Published Event Shape (Redis → Gateway → Browser)

After filtering, events are published to Redis channel `tca:events`
as JSON. This is the shape the gateway already consumes:

```json
{
  "id": "string",            // UUID assigned by this service
  "timestamp": "string",     // ISO 8601 UTC
  "caller": "string",
  "callee": "string",
  "method": "string",
  "path": "string",          // Sanitized
  "statusCode": 200,         // camelCase to match existing frontend contract
  "latencyMs": 12,           // camelCase to match existing frontend contract
  "protocol": "string"
}
```

Note: inbound uses `snake_case` (idiomatic for inter-service JSON),
published uses `camelCase` to match the existing frontend contract.
Transformation happens here.

---

## Additional Endpoints

### GET /health
```json
{
  "status": "healthy",
  "service": "observability-service",
  "language": "Go",
  "redis": "connected | disconnected",
  "events_received": 1042,
  "events_published": 1038,
  "events_dropped": 4
}
```

Counters are in-memory, reset on restart. Useful for spotting
drop rates during demos.

### GET /rules
Returns the active filter rules and service allowlist.
Useful for debugging unexpected drops.

```json
{
  "service_allowlist": ["gateway", "game-state", "..."],
  "method_allowlist": ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD"],
  "protocol_allowlist": ["http", "sse", "websocket", "mtls"],
  "path_sanitization": [
    {"pattern": "IPv4", "replacement": "[ip]"},
    {"pattern": "UUID", "replacement": "[id]"},
    {"pattern": "JWT", "replacement": "[token]"},
    {"pattern": "query values", "replacement": "keys only"}
  ]
}
```

---

## How Services Report Events

Each service wraps its outbound HTTP calls with a thin reporter:

```
before call  → record start time
make call    → get status code
after call   → POST /event to observability-service (non-blocking goroutine / async)
```

The reporter is fire-and-forget. If observability-service is down,
the call is logged locally and dropped. Game logic is unaffected.

Each language needs one small helper:
- **Go** — goroutine with http.Post
- **Python** — threading.Thread with requests.post
- **Java** — CompletableFuture with HttpClient
- **TypeScript** — fetch with no await
- **Haskell** — forkIO with http-client
- **Elixir** — Task.start with HTTPoison

---

## Gateway Changes

Gateway currently builds its own ObservabilityBus from proxy traffic.
After this change:

1. Gateway proxy calls still publish to the local bus (gateway → downstream events)
2. Gateway also subscribes to Redis `tca:events` channel
3. Redis events are fed into the same local bus
4. SSE handler unchanged — still fans out from the local bus

Net effect: browser sees all events — gateway-proxied and internal —
through the same SSE stream.

---

## Docker / Infrastructure

```yaml
observability-service:
  build: ./observability-service
  container_name: tca-observability
  environment:
    PORT: "3009"
    REDIS_URL: "redis:6379"
  networks:
    - tca-net
  # Not exposed externally — internal only
  depends_on:
    - redis
```

No external port exposure. Only reachable by other services on tca-net.

---

## What's Stubbed (PoC)

Nothing. This service is simple enough to implement fully:
- Real Redis pub/sub (go-redis client)
- Real filtering and sanitization
- Real allowlist enforcement
- Counters for health endpoint

The only "stub" is that advanced filtering rules (ML-based PII detection,
rate limiting per service) are left as future work with clear extension points.
