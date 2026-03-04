# TCA Observability Job — Reference

**Status:** Living Document  
**Last Updated:** 2026-03-04  
**Audience:** Engineers and AI partners implementing TCA projects  
**Scope:** Architecture, contract, implementation, and integration of the Observability Job

---

## The Heartbeat

The Contract is the lynchpin of TCA — the thing that holds everything together. Observability is the heartbeat. You can have a perfectly contracted, correctly isolated, securely communicating TCA application and still have no visibility into what it is actually doing at runtime. Every inter-service call happens inside mTLS-encrypted channels on an isolated internal network. Without observability, that network is a black box. You know the services are running. You don't know if they're talking.

The Observability Job makes every inter-service call visible — authenticated, sanitized, and streamed in real time — without coupling any service to any other service and without adding anything to the critical path of a single request.

It started as a development aid. The value of watching the swarm communicate while building it was immediately apparent. By the first day it was running, it had already graduated from "nice to have during development" to "this is infrastructure." The security visibility alone — a real-time authenticated feed of all inter-service traffic — justifies it in any production deployment.

---

## What It Does

```
[any Job] → POST /event (fire and forget) → [Observability Job]
                                                      ↓
                                          allowlist validation
                                                      ↓
                                          path sanitization
                                                      ↓
                                          Redis pub/sub → tca:events
                                                      ↓
                                          [Gateway] → SSE → [Dashboard]
                                                      ↓
                                          [any authorized subscriber]
```

Every Job in the application wraps its outbound HTTP calls with a thin, non-blocking reporter. After each call completes, the reporter fires a POST to the Observability Job with seven fields: who called whom, what method, what path, what status, how long it took, and what protocol. The reporter does not wait for a response. The game does not wait for the reporter. If the Observability Job is unreachable, the reporter logs locally and moves on.

The Observability Job receives the report, validates it against allowlists, sanitizes sensitive data from the path, assigns a UUID and timestamp, translates to camelCase for the frontend contract, and publishes to a Redis pub/sub channel. The Gateway subscribes to that channel and fans events to connected SSE clients. Any authorized service account can subscribe independently via the `/event` endpoint.

No Job knows who is consuming its reports. No Job knows Redis exists. They report to a single endpoint and stop thinking about it.

---

## Why Fire-and-Forget

Observability must never be on the critical path. This is not a performance preference — it is an architectural requirement.

If a reporting call blocks game logic, or if the Observability Job going down causes game requests to fail, then the monitoring system has become a dependency of the system it is monitoring. That inverts the relationship. A monitoring system that can take down the application is not a safety net — it is a liability.

Fire-and-forget means: the caller dispatches the report in a separate goroutine (or thread, or async task — the language determines the mechanism), does not inspect the response, does not retry on failure, and does not alter its behavior based on whether the report succeeded. The 202 response the Observability Job always returns is a courtesy acknowledgment, not a confirmation the caller should wait for.

**The implementation pattern in Go:**

```go
func reportEvent(callee, method, path string, status int, latencyMs int64) {
    go func() {
        body, _ := json.Marshal(map[string]interface{}{
            "caller":      "service-name",  // hardcoded to this service's name
            "callee":      callee,
            "method":      method,
            "path":        path,
            "status_code": status,
            "latency_ms":  latencyMs,
            "protocol":    "mtls",
        })
        resp, err := http.Post(observabilityURL+"/event", "application/json", bytes.NewReader(body))
        if err != nil {
            log.Printf("[observability] report error: %v", err)
            return
        }
        resp.Body.Close()
    }()
}
```

The pattern in other languages follows the same structure:
- **Python:** `threading.Thread(target=_report, daemon=True).start()`
- **Java:** `CompletableFuture.runAsync(() -> report(...))`
- **TypeScript:** `fetch(url, opts)` with no `await`
- **Haskell:** `forkIO (report ...)`
- **Elixir:** `Task.start(fn -> report(...) end)`

In every case: launch asynchronously, do not await, do not check the result. The wrapping is thin enough that it can be expressed as a one-liner at the call site.

---

## Why Filter at Ingress

All sanitization of incoming event data happens inside the Observability Job, before anything is published to Redis or reaches the dashboard. Individual Jobs do not sanitize before reporting.

**The reason this matters:** If sanitization were the responsibility of each reporting Job, it would need to be implemented correctly in six different languages, maintained in twelve different codebases, and verified to be consistent across all of them. One Job that forgets to strip UUIDs from a path means user IDs are flowing through Redis to the browser. Centralizing sanitization means it happens once, in one place, in one language, and every Job in the system benefits from it automatically.

The sanitization rules applied to the `path` field before publishing:

| Pattern | Replacement | Reason |
|---------|-------------|--------|
| JWT tokens (`eyJ...`) | `[token]` | Credential leak via dashboard |
| IPv4 addresses | `[ip]` | Internal topology exposure |
| IPv6 addresses | `[ip]` | Internal topology exposure |
| UUIDs | `[id]` | User ID reduction / noise |
| Query string values | key names only, values → `[redacted]` | Values may contain PII or session data |

The `caller` and `callee` fields are not sanitized — they are validated against the service allowlist and either pass through as-is or cause the event to be dropped entirely.

---

## Why the Allowlist Drops Silently

Events from unknown callers or callees are dropped with a 202 response and a local log entry. The caller receives no error. This is intentional.

The allowlist exists to prevent a compromised service from injecting arbitrary names into the observability feed. If a service is compromised and begins posting fabricated events claiming to be from services that don't exist, a noisy rejection response gives the attacker feedback that helps them probe the allowlist. Silent drops give them nothing.

The `/rules` endpoint exists to help engineers debug unexpected drops during development — it returns the active allowlist and sanitization configuration. This endpoint is internal-only. The information it exposes is useful for debugging and would also be useful for an attacker trying to craft events that pass validation, so external exposure is not appropriate.

**Adding a service to the allowlist** is done in two places:
1. The Observability Job's `knownServices` map in implementation code
2. The known service allowlist documented in the `/event` path description in the contract

Both must be updated together. A service that reports to the Observability Job but is not in the allowlist will have all its events silently dropped, with no error surfaced to the reporting service. If inter-service traffic is visible in the dashboard except for one service, this is the first thing to check.

---

## The Contract

The Observability Job contract is designed to be dropped into any TCA implementation as-is. The only project-specific configuration is the service allowlist — update it to reflect the Jobs in your application.

**`contracts/openapi/observability-service.yaml`** — full contract, reference implementation in the TCA Blackjack repository.

Key contract properties:

**`/event` always returns 202.** Even if the event is dropped due to allowlist validation or sanitization. Callers see a uniform response regardless of what happened to their report. This is consistent with fire-and-forget: callers are not equipped to handle differentiated responses, and differentiating them would add complexity for no benefit.

**`id` and `timestamp` are assigned by the Observability Job, not the caller.** Callers do not generate UUIDs or timestamps for their reports. This ensures consistent timestamp format across all events regardless of the reporting service's language or clock precision, and prevents callers from manipulating event ordering.

**`snake_case` inbound, `camelCase` published.** Inbound reports use `snake_case` (idiomatic for inter-service JSON across Go, Python, and most other languages in the stack). Published events use `camelCase` to match the existing frontend contract. The transformation happens inside the Observability Job, once. No other Job needs to know this distinction exists.

**The `x-tca-observability` block is absent from the Observability Job's own contract.** Every other Job reports its outbound calls to the Observability Job. The Observability Job does not report its own calls — doing so would create a reporting loop. The absence of the block is intentional and must not be added.

---

## Infrastructure Requirements

The Observability Job has two infrastructure dependencies:

**Redis** — for pub/sub. The Job publishes to channel `tca:events`. The Gateway subscribes to this channel. Redis is an implementation detail of the Observability Job — no other service interacts with it for observability purposes.

```yaml
# docker-compose excerpt
observability-service:
  build: ./observability-service
  container_name: tca-observability
  environment:
    PORT: "3009"
    REDIS_URL: "redis:6379"
  networks:
    - tca-net
  depends_on:
    - redis

redis:
  image: redis:7-alpine
  networks:
    - tca-net
```

No external port exposure. The Observability Job is reachable only by services on `tca-net`. The Gateway's SSE endpoint is the external surface for the event stream.

**Gateway Redis subscription** — the Gateway subscribes to `tca:events` and fans events to SSE clients. This requires a Redis client in the Gateway implementation in addition to the standard mTLS proxy logic. The SSE handler is unchanged — it fans from the local event bus regardless of event source.

---

## Implementation Notes

**Scratch container.** The Observability Job compiles to a static Go binary and runs in a scratch container — no shell, no package manager, no OS utilities, no attack surface beyond the binary itself. This is the correct container choice for a service that receives traffic from every other service in the application and sits upstream of the dashboard. If this service is compromised, an attacker has visibility into all inter-service communication patterns. Minimize the attack surface accordingly.

**In-memory counters.** The health endpoint reports `events_received`, `events_published`, and `events_dropped` since last restart. These are atomic in-memory counters — they reset on restart and are not persisted. They are useful for spotting drop rates during development and demos. The ratio of received to published to dropped is the diagnostic signal: a growing drop count during normal operation means something is posting events with names not in the allowlist.

**Redis retry on startup.** The Observability Job retries the Redis connection on startup rather than failing immediately. Container orchestration does not guarantee Redis is ready before the Observability Job starts. Ten retries at two-second intervals is sufficient for Docker Compose; Kubernetes readiness probes handle this more gracefully.

**The `/rules` endpoint.** Returns the active filter configuration — allowlists and sanitization patterns. This is a development and debugging tool. During normal operation it is not called. If events from a specific service are not appearing in the dashboard, `/rules` confirms what the allowlist contains and whether the service name is correctly registered.

---

## Adding the Observability Job to a New TCA Project

1. **Copy the contract** from `contracts/openapi/observability-service.yaml`. Update the service allowlist in the `/event` path description to reflect the Jobs in your project.

2. **Copy the implementation** from `observability-service/`. Update the `knownServices` map to match your project's service names. No other changes are required.

3. **Add Redis** to your Docker Compose or Kubernetes configuration.

4. **Update the Gateway** to subscribe to `tca:events` on Redis and fan events to its SSE endpoint.

5. **Add the `x-tca-observability` block** to every other Job's contract. The block is identical across all Jobs except for the `caller-name` field, which must match the service's name in the Observability Job's allowlist exactly.

6. **Implement the reporter function** in each Job. The pattern is the same across all languages: wrap every outbound HTTP call, record start time before the call, capture status and elapsed time after, dispatch the report asynchronously.

The contract is portable. The implementation is portable. The only project-specific work is registering service names in two places and wiring the reporter into each Job's outbound calls.

---

## What the Dashboard Shows

The Observability Job does not define the dashboard — that is the Gateway's SSE endpoint and the frontend's concern. What it guarantees is the event stream that feeds any visualization built on top of it.

Each published event contains: timestamp, caller service, callee service, HTTP method, sanitized path, response status code, latency in milliseconds, and protocol. This is enough to visualize:

- Which services are communicating and how frequently
- Latency distribution across service boundaries
- Error rates by service pair and endpoint
- Traffic patterns over time
- Which services are silent (potential health signal)

A security team with a service account subscribed to the `/event` endpoint gets the same feed — authenticated, encrypted, sanitized, formatted before it reaches them. This is not a bolt-on security feature. It is a consequence of the architecture.
