# TCA Blackjack

**Proof-of-Concept: Polyglot Microservices · Zero Trust · AI-Augmented Development**

## What is Tessellated Constellation Architecture (TCA)

I'm working on a white paper to explain the Tessellated Constellation Architecture concept in detail, but I'll try to cover it briefly here. I've been working on developing how the IT arena should be working with AI. Not theorizing. But developing working examples of what can be done. Not what might work. What does work. I did my first example here: https://github.com/Pelentan/lora-dataset-prep. In this project I wanted to try out what I thought was a whole new way of creating an application that was only feasible with an AI partner. Ah… Age and memory. It was only when I finished and looked closer that I realized I was standing on the shoulders of giants. Almost every aspect of this architecture has been around for a while and I was only half-remembering them. What I brought was the extraction of the core ideas and principles of all these different concepts and combining them together in a cohesive, repeatable, architecture. One that I would argue was impossible last year. And required this year.

The initial concept was an application broken down into discrete Jobs. Ones that have a fixed set of inputs and a fixed set of outputs that never changed. And didn't care where they were coming from or going to. Many might recognize this as "micro-services."

That brought the first blast from the past: contracts. And not something that was written after the code to describe what it does. But rather created before the first line of code and held to. During initial development some drift was allowed as I realized new things needed to be added. But the core required to be the same. This also means that a developer, human or AI, doesn't need to know anything more than that one Job. Their entire world is encapsulated in that contract. And the rest of the universe doesn't give a damn what that developer does inside that Job, just as long as the output part of that contract is adhered to.

Which brought the next concept. Best language for the Job. I had already made the decision that all Jobs were going to be in their own container. So, the architectural hurdle was already solved. But that left knowing how to program all the different languages that might be pulled in. I've often said that if you know how to code, you basically know how to code any modern language. And I've proven that out repeatedly. That doesn't mean knowing how to code well or fast. AI is the answer here. A good AI doesn't care what language you want something written in. As long as it's in its training, or it can look it up, it will write code that is often more correct than what a human could do. Not necessarily better. But working and following all the rules. And in sticking to small, discrete Jobs, it doesn't need to be super-efficient.

The final concept was ultra secure. At the start the interfaces were just stubbed. But those stubs were part of the architecture and treated just like any other Job. The contract was built first and not changed. After the application was going, it was child's play to add the actual code that handles security.

The bonus was observability. The security geeks better be salivating at this. Normally, for communication inside an application, you have to rely on coders to put log statements at various places. Not now. While this doesn't provide the level of visibility you may fantasize of having, you now have observability on all traffic between different Jobs. All secure with mTLS, but visible with the right key. What Claude and I built was authenticated, encrypted, observable inter-service traffic.

Those are what I went into this project with. Like the last one, joined at the hip with Claude.ai. It started with concepting things out, picking the initial languages, and then jumping right in. In less than 40 hours we produced what you see here. A working prototype. Mind you, I'm calling this "project-complete" and not "feature-complete" but only because features like multi-player and multi-table weren't implemented. They weren't needed for what this project's focus was:

*Tessellated Constellation Architecture: Human and AI working together to make the visions of giants a reality.*

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

See `infra/tca-architecture.html` for the full visual diagram.

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

TCA Blackjack implements a **mandatory passkey enrollment** model. Passwords do not exist in this system.

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
- That's it. mTLS certificates are generated automatically at startup by the `cert-init` container.

### Email Setup

The email service requires SMTP credentials to send verification emails. Copy the example and fill in your values:

```bash
cp .env.example .env
```

**For Gmail (recommended for local testing):**
1. Enable 2-Step Verification on your Google account
2. Generate an App Password at https://myaccount.google.com/apppasswords
3. Fill in `.env`:

```
SMTP_RELAY_HOST=smtp.gmail.com
SMTP_RELAY_PORT=587
SMTP_USER=you@gmail.com
SMTP_PASSWORD=your-16-char-app-password
SMTP_FROM=you@gmail.com
```

**For a corporate relay:**
```
SMTP_RELAY_HOST=mail.yourcompany.com
SMTP_RELAY_PORT=25
SMTP_USER=
SMTP_PASSWORD=
SMTP_FROM=noreply@yourcompany.com
```

Without a `.env` file, the email service starts but verification emails go nowhere. The registration flow will complete but the verification link won't arrive. Everything else works fine.

### First Run

```bash
# Build and start all services (from project root)
docker compose up --build
```

Certificates are generated by `cert-init` before any service starts. No manual steps.

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

2. **Observability panel** (drawer at the bottom of the screen): every inter-service call visible in real-time — caller, callee, protocol, latency, status. Container hostname on each game state update shows which instance handled it. Pull the drawer up to watch the constellation in action.

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
| Chat Service | ✅ Built | Elixir/OTP with mTLS, full message history per table — multi-player not implemented for this PoC |
| Email Service | ✅ Functional | Real SMTP via Postfix/Gmail relay, mTLS |
| Observability | ✅ Functional | Live service call feed, Redis pub/sub, dashboard, mTLS |
| Document Service | ✅ Functional | PDF generation via PDFKit, TypeScript |
| Redis | ✅ Functional | Sessions, exchange codes, verify tokens, observability pub/sub, balance pub/sub |
| mTLS | ✅ Complete | Ephemeral PKI via init container — all 12 internal services enforcing mutual TLS |
| API Contracts | ✅ Complete | 12 OpenAPI 3.1 YAML files in contracts/openapi/ |
| OPA Integration | ⚠️ Stub | Token scope enforced at gateway; OPA policy engine stubbed (always authorized) |
| Multi-table | ⚠️ Pending | Architecture supports it, UI shows one table |
| Network isolation | ⚠️ Pending | Single tca-net now; per-domain networks next phase |

---

## Development Methodology

This project was built through AI-augmented development — a human-AI partnership where architectural decisions, security design, and engineering judgment came from human expertise, and implementation was accelerated by an AI collaborator fluent across the full stack.

The goal: demonstrate that experienced engineers working with AI can achieve significant productivity gains while maintaining — and in some cases exceeding — the quality standards of traditional development.

**The numbers:** 12 containerized services across 6 languages, full mutual TLS across the stack, WebAuthn passkey authentication, COBOL financial arithmetic, real-time observability, real email delivery, 12 OpenAPI contracts — built in **under 40 hours of actual working time** across approximately one calendar week. A team would budget 3–4 months for a system of this complexity and architectural coherence.

The leverage comes from the engineer providing what AI cannot: architectural vision, security threat modeling, domain judgment, and the experience to recognize when something is wrong before it becomes expensive to fix. The AI provides what the engineer shouldn't have to do manually: fluent implementation across 6 language ecosystems simultaneously, without the inconsistency that leads to vulnerabilities.

Security scanning (CodeQL, secret scanning, dependency review, Dockerfile linting) was the first commit. That principle holds for every project.