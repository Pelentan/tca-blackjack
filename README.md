# Swarm Blackjack

**Proof-of-Concept: Polyglot Microservices · Zero Trust · AI-Augmented Development**

A fully functional blackjack application built to demonstrate modern swarm architecture — discrete services in isolated containers, each written in the best language for its job, communicating via REST and SSE, with a working Zero Trust authentication chain built on passkeys.

> *"AI amplifying expertise, not replacing engineers."*  
> This codebase was architected and implemented through AI-augmented development. The architecture decisions, security design, and engineering judgment came from human expertise. The implementation was accelerated by an AI partner fluent in every language in the stack.

---

## Architecture

```
Browser → UI (React/TypeScript)
       → API Gateway (Go)              ← single external entry point, scope enforcement
           → Auth UI Service (Go)      ← server-driven auth forms, passkey ceremony proxy
           → Auth Service (TypeScript) ← WebAuthn/passkey, JWT issuance, session management
               → Auth DB (PostgreSQL)
               → Redis                 ← sessions, exchange codes, verify tokens
           → Game State (Go)           ← SSE, state machine, demo loop
               → Deck Service (Go)
               → Hand Evaluator (Haskell)
               → Dealer AI (Python)
               → Bank Service (COBOL+Go)
               → Game DB (PostgreSQL)
           → Bank Service (COBOL+Go)   ← financial arithmetic in COBOL, REST API in Go
               → Bank DB (PostgreSQL)
           → Chat Service (Elixir)
           → Email Service (Python)    ← real SMTP via Postfix/Gmail relay
           → Observability Service (Go)
               → Redis                 ← pub/sub for internal service events
```

See `infra/swarm-architecture.html` for the full visual diagram.

---

## Why Each Language

| Service | Language | Reason |
|---|---|---|
| Gateway | Go | Throughput, HTTP handling, concurrency. Built for this. |
| Auth UI | Go | Server-driven form flow. Lightweight proxy to auth-service. |
| Game State | Go | State machine, goroutine-per-connection for SSE. |
| Deck Service | Go | Pure computation, speed at scale. |
| Hand Evaluator | **Haskell** | Pure function. Cards in → value out. Haskell's type system makes it *provably correct* — the compiler enforces no side effects. This is exactly the use case Haskell was designed for. |
| Dealer AI | Python | Rule-based today. The architecture leaves a clean ML upgrade path — swap the decision function, keep the endpoint contract. Python's ML ecosystem is unmatched. |
| Bank Service | COBOL + Go | Financial arithmetic in COBOL — integer cent arithmetic, no floats near money. Go provides the REST API layer and PostgreSQL integration. |
| Auth Service | TypeScript | WebAuthn/passkey ecosystem deepest here. `@simplewebauthn/server` v10 is the gold standard. |
| Chat Service | Elixir | This is literally what it was designed for. WhatsApp runs on it. OTP supervision trees: a crashed process restarts in isolation, never takes down the game. |
| Email Service | Python | Real SMTP delivery via Postfix. Swap transport with zero upstream changes. |
| UI | React/TypeScript | Component architecture. Portable to Electron desktop with zero business logic changes. |

### Why Not Rust?

Rust is frequently positioned as the "safe systems language." We evaluated it and ruled it out. Our selection criteria:

1. **Works** — battle-tested in production at scale
2. **Secure** — supply chain, governance, ecosystem trust
3. **Best for the job** — fits the problem domain
4. **Isolated** — replaceable independently

Rust failed criterion 2. The Rust governance structure and documented ideological filtering in core contributor selection creates supply chain trust concerns that outweigh its memory safety advantages — particularly in environments where you cannot fully audit the compiler chain itself.

Where Rust's primary value proposition is memory safety, this architecture addresses that differently: the AI-augmented development methodology eliminates the human inconsistency factors that typically cause memory safety vulnerabilities. The result is safer code without the governance risk.

---

## Security Architecture

### Authentication — Passkeys Required

Swarm Blackjack implements a **mandatory passkey enrollment** model. Passwords do not exist in this system.

**The two-token bootstrap chain:**

```
1. Register → email verification link sent
2. Click link → exchange code → bootstrap JWT (scope: "enroll", 5min TTL, memory-only)
3. Bootstrap JWT → WebAuthn ceremony → OS prompts for biometric/PIN
4. Ceremony succeeds → session JWT (scope: "session", 15min TTL, localStorage)
5. Session JWT → play
```

The bootstrap token proves email ownership. It cannot be used to access game routes — the gateway rejects it with 403 (not 401) on any non-enrollment endpoint. The session JWT is only issued after a successful passkey ceremony. There is no path to a session JWT that bypasses the passkey.

**Subsequent logins:** passkey ceremony only. One tap, no passwords, no codes.

**Why passkeys over passwords:**
- Private key never leaves the device (TPM / Secure Enclave)
- Phishing-resistant by design — assertions are cryptographically bound to the origin
- No shared secret on the server — a database breach exposes nothing useful
- No TOTP codes to intercept in real-time MITM attacks

**Hardware token vs. platform passkey tradeoff:**  
Hardware tokens (YubiKey) add hardware-enforced possession and explicit touch confirmation per assertion. Platform passkeys with TPM backing are equivalent for most threat models. The gap matters for employees with production system access or regulated environments requiring FIDO2 hardware attestation. For consumer applications, platform passkeys are the right answer.

### Sessions

- Short-lived JWTs (15 minutes) with explicit `scope` claim (`enroll` | `session`)
- Redis refresh tokens — instant revocation, "sign out everywhere" works
- Gateway enforces scope at the routing layer — bootstrap tokens cannot reach game or bank routes
- Bank service re-validates Redis session on every financial operation regardless of JWT validity

### Zero Trust Posture

Every user request crosses a trust boundary at the gateway. Scope enforcement happens at the proxy layer before requests reach services.

**Service-to-service: mutual TLS**  
Internal calls use mTLS on the isolated Docker network. Identity verification and encrypted transit without policy overhead.

> *"Sometimes you gotta shake hands without gloves."*  
> Zero Trust is a posture, not a religion. The threat model for an internal container-to-container call on an isolated Docker network is fundamentally different from a user request crossing a trust boundary.

### Why SSE for game state, WebSocket for chat?

- **Game State → SSE**: Game state is server-driven. The client receives updates; it doesn't push them. SSE is standard HTTP — your WAF, rate limiter, and auth middleware all work normally. Client actions are discrete authenticated POST requests.
- **Chat → WebSocket**: Chat is genuinely bidirectional. Players send and receive simultaneously. WebSocket is the correct tool.

---

## Running Locally

### Prerequisites

- Docker Desktop (or Docker Engine + Compose)
- `openssl` (for cert generation — standard on macOS/Linux)

### First Run

```bash
# Generate mTLS certificates
chmod +x infra/scripts/gen-certs.sh
./infra/scripts/gen-certs.sh

# Build and start all services (from project root)
docker compose up --build
```

### Access Points

| URL | What |
|---|---|
| http://localhost:8021 | Game UI + Observability Dashboard |
| http://localhost:8021/health | Gateway health (all upstream status) |
| http://localhost:8021/events | Observability SSE feed (raw) |

### Service Health Checks

```bash
# All services
docker compose ps

# Individual service logs
docker compose logs -f game-state
docker compose logs -f auth-service

# Gateway health (shows all upstream status)
curl http://localhost:8021/health | jq
```

### Development Reset

The UI has a **⚠ Reset DB** button in the header. It wipes all player accounts, passkey credentials, sessions, and bank balances, then re-seeds the demo player. Useful during development to test the full registration flow repeatedly without managing database state manually.

---

## The Demo Story

### What you'll see

1. **Game loop** (no auth required): SSE connection established, demo table cycles automatically through betting → dealing → player turn → dealer turn → payout. Every phase triggers calls to Deck (Go), Hand Evaluator (Haskell), Dealer AI (Python), Bank (COBOL+Go).

2. **Observability panel**: every inter-service call visible in real-time — caller, callee, protocol, latency, status. Container hostname on each game state update shows which instance handled it.

3. **Auth flow** (Login / Register button): full passkey ceremony — register with email, verify via link, OS prompts for biometric/PIN, enrolled and signed in. Subsequent logins: one tap.

### What this demonstrates

- **Polyglot works**: 6 languages, one coherent system
- **Zero Trust is implementable**: mandatory passkey enrollment, scoped tokens, gateway enforcement — not a whitepaper, a working system
- **Observability by design**: inter-service traffic visible without instrumentation changes
- **Replaceability**: each service rewritable independently — Hand Evaluator could be replaced with Go tomorrow, Game State doesn't care
- **The 3-year lifecycle**: small discrete services with clean contracts make complete rewrites feasible, expected, and healthy
- **AI-augmented development**: architecture, security design, and judgment from the engineer; implementation accelerated across the full polyglot stack by AI

---

## K8s Migration Path

Each service is already a deployable unit. The migration from Compose to K8s is mechanical:

- Docker Compose `services:` → K8s `Deployment` objects
- Compose `networks:` → K8s `NetworkPolicy`
- Compose `volumes:` → K8s `PersistentVolumeClaim`
- OPA as sidecar policy engine (already designed for this)
- Horizontal pod autoscaling per service — scale deck-service independently of game-state

**Service grouping by latency sensitivity:**
- Game domain (Game State, Deck, Hand Evaluator) — hard affinity, same node
- Security/Financial domain (Auth, Bank) — co-located with each other, independent from game loop
- Communication domain (Chat, Email) — float freely
- OPA — true sidecar to Auth, same Pod (policy evaluation on every user request warrants loopback)

No architectural changes required for K8s. That was the point.

---

## Project Status

| Service | Status | Notes |
|---|---|---|
| API Gateway | ✅ Functional | Routing, SSE proxy, scope enforcement, mTLS upstream |
| Auth Service | ✅ Functional | Full passkey/WebAuthn ceremony, JWT issuance, sessions, mTLS |
| Auth UI Service | ✅ Functional | Server-driven forms, passkey ceremony proxy, mTLS to auth-service |
| Auth DB | ✅ Functional | players, passkey_credentials, webauthn_challenges |
| Game State | ✅ Functional | SSE, demo loop, full service orchestration, mTLS |
| Deck Service | ✅ Functional | Real shuffle/deal, penetration threshold, reshuffle, mTLS |
| Hand Evaluator | ✅ Functional | Pure Haskell, soft/hard totals, bust detection, mTLS |
| Dealer AI | ✅ Functional | Rule-based strategy, ML upgrade path preserved, mTLS |
| Bank Service | ✅ Functional | COBOL financial arithmetic, PostgreSQL persistence, Redis balance pub/sub, mTLS |
| Chat Service | ⚠️ REST only | Elixir/OTP running with mTLS — WebSocket upgrade pending |
| Email Service | ✅ Functional | Real SMTP via Postfix/Gmail relay, mTLS |
| Observability | ✅ Functional | Live service call feed, Redis pub/sub, dashboard, mTLS |
| Document Service | ✅ Functional | PDF generation via PDFKit, TypeScript |
| Redis | ✅ Functional | Sessions, exchange codes, verify tokens, observability pub/sub, balance pub/sub |
| mTLS | ✅ Complete | Ephemeral PKI via init container — all 12 internal services enforcing mutual TLS |
| API Contracts | ✅ Complete | 12 OpenAPI 3.1 YAML files in contracts/openapi/ |
| OPA Integration | ⚠️ Stub | Token scope enforced at gateway; OPA policy engine stubbed (always authorized) |
| Multi-table | ⚠️ Pending | Architecture supports it, UI shows one table |
| Network isolation | ⚠️ Pending | Single swarm-net now; per-domain networks next phase |

---

## Development Methodology

This project was built through AI-augmented development — a human-AI partnership where architectural decisions, security design, and engineering judgment came from human expertise, and implementation was accelerated by an AI collaborator fluent across the full stack.

The goal: demonstrate that experienced engineers working with AI can achieve significant productivity gains while maintaining — and in some cases exceeding — the quality standards of traditional development.

**The numbers:** 12 containerized services across 6 languages, full mutual TLS across the stack, WebAuthn passkey authentication, COBOL financial arithmetic, real-time observability, real email delivery, 12 OpenAPI contracts — built in **under 40 hours of actual working time** across approximately one calendar week. A team would budget 3–4 months for a system of this complexity and architectural coherence.

The leverage comes from the engineer providing what AI cannot: architectural vision, security threat modeling, domain judgment, and the experience to recognize when something is wrong before it becomes expensive to fix. The AI provides what the engineer shouldn't have to do manually: fluent implementation across 6 language ecosystems simultaneously, without the inconsistency that leads to vulnerabilities.

Security scanning (CodeQL, secret scanning, dependency review, Dockerfile linting) was the first commit. That principle holds for every project.
