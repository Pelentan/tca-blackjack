# Swarm Blackjack — Development Story

**A case study in AI-augmented software development**

---

## What This Is

Swarm Blackjack is a proof-of-concept polyglot microservices architecture built to answer a specific question: *Can a single experienced engineer, working with an AI partner, produce the kind of sophisticated distributed system that would normally require a large team?*

The answer, documented here, is yes — with important nuance about what that actually means.

This document is not the technical reference (see `README.md` for that). This is the story of how it was built, what decisions were made and why, what went wrong and how it was fixed, and what we learned. It is written this way intentionally — transparency about the process is the point.

---

## The Setup

**One engineer. One AI partner. No sprint planning. No standups.**

The engineer brings 20+ years of experience across Go, PHP, Python, Java, Node/React, Docker, Kubernetes, and enterprise security architecture including HIPAA-compliant systems for the Veterans Health Administration. The architectural judgment, security design, and engineering standards came entirely from that experience.

The AI partner (Claude, Anthropic) handles implementation across every language in the stack simultaneously. It does not replace engineering judgment — it amplifies it.

The working model throughout:
- Engineer defines what needs to exist and why
- Engineer and AI validate the approach before any code is written
- AI implements; engineer reviews, tests, and directs corrections
- Engineer catches what AI misses; AI catches what engineer misses

---

## The Stack Decision

The first architectural decision was also the most deliberate: **use the right language for each service, not one language for everything.**

This is a choice most teams avoid because maintaining expertise across six languages is organizationally expensive. With an AI partner fluent in all of them, that constraint disappears. The result:

| Service | Language | The actual reason |
|---|---|---|
| Gateway, Game State, Deck Service | Go | Built for high-throughput HTTP and concurrency |
| Hand Evaluator | Haskell | Pure function, provably correct — the compiler enforces no side effects |
| Dealer AI | Python | Rule-based today, clean ML upgrade path tomorrow |
| Bank Service | COBOL + Go | Integer cent arithmetic in COBOL — no floats near money. Go provides REST API and PostgreSQL layer. |
| Auth Service | TypeScript | Deepest WebAuthn/passkey ecosystem |
| Chat Service | Elixir | OTP supervision trees — this is literally what it was designed for |
| Email Service | Python | Simplest implementation; swap transport, zero upstream changes |

**Why not Rust?** We evaluated it. The memory safety argument is real but the governance structure of the Rust project — documented ideological filtering in core contributor selection — creates supply chain trust concerns that matter in security-sensitive environments. This architecture addresses memory safety differently: the AI-augmented development methodology eliminates the human inconsistency factors that cause most memory safety vulnerabilities. An architectural solution beats a language-level one when the architecture is sound.

---

## What Got Built

### Phase 1 — Working Skeleton
A complete running system: all 9 services containerized, game loop functional, cards dealing, SSE streaming game state to the browser, observability dashboard showing live inter-service calls. Most services were stubs — correct architecture, minimal implementation. Proved the approach works.

### Phase 2 — Real Implementations
Services promoted from stub to functional one by one:

**Bank Service** — authoritative chip balances with `BigDecimal` arithmetic. Game State delegates all financial operations here. Bet → payout flow goes through an explicit transaction ID, preventing double-settlement.

**Auth Service** — real JWT issuance, Redis session storage, instant revocation. A separate Auth UI service (Go, scratch container) sits between the browser and the auth service — defense in depth. A compromised frontend-facing service can't directly touch JWT issuance.

**Email Service** — five-tier security architecture (system/social/personal/confidential/restricted) with an OPA authorization check on every send. Honeypot patterns built in — `password_reset` as a message type is in the registry but the auth layer catches and rejects it before it renders, because this system doesn't have passwords.

**Email Verification Flow** — registration sends a real email. Session is not issued until the link is clicked. The verification link goes through the gateway (`/verify?token=`) which routes internally — the browser never needs to know the auth service exists. Exchange code pattern: verify token → 60-second single-use exchange code → JWT. No credentials ever appear in a URL.

**Postfix MTA** — real outbound email delivery. Containerized Postfix on Debian, configured as a send-only relay. No third-party email service required. For local development, relays through Gmail SMTP with app password credentials stored in `.env` (gitignored). In production deployment on infrastructure with a proper PTR record, direct MX delivery works without a relay.

---

## Key Decisions and Why

### Contract-First Development
OpenAPI specs were written before implementations. The UI was built against contracts, not running services. This is why everything composes cleanly — services can be replaced independently because the contracts are the source of truth, not the implementations.

### SSE for Game State, WebSocket for Chat
Not one technology applied everywhere. Game state is server-driven — clients receive updates, they don't push them. SSE is standard HTTP: your WAF, rate limiter, and auth middleware all work normally. Chat is genuinely bidirectional. Using WebSocket for game state would be like using a walkie-talkie when you only ever need to listen to the radio.

### Scratch Containers for Go Services
Static Go binaries compiled with `CGO_ENABLED=0` have no runtime dependencies. The correct target is `scratch`, not Alpine. Zero OS means zero OS attack surface. Ca-certificates are copied in for TLS; nothing else is needed.

### The Alpine Question
Mid-development we audited all Dockerfiles and migrated everything off Alpine. This conversation is worth documenting:

Alpine's appeal is image size. But musl libc (Alpine's C library) causes subtle compatibility issues with anything that assumes glibc — which is most of the software world. We discovered this the hard way when containerized Postfix on Alpine failed to stay running due to syslog dependency issues that don't exist on Debian. The fix was switching to `debian:bookworm-slim`.

The rule we landed on: **static compilation languages go to scratch; everything else goes to Debian slim.** Alpine is the wrong answer to the right instinct. Storage is cheap. Compatibility problems are expensive.

The migration touched 8 Dockerfiles, required cross-cutting knowledge of container internals, libc compatibility, static binary behavior, and Postfix startup behavior across distros — all applied simultaneously across a polyglot stack. Wall clock time: approximately 5 minutes. Estimated time for a human developer with equivalent knowledge: half a day minimum, likely longer due to context-switching between ecosystems. This is where AI amplification is most pronounced.

### Credentials and `.env`
Real credentials (SMTP app password) are stored in a `.env` file that is gitignored. A `.env.example` with placeholders is committed. This is standard practice documented here explicitly because it's how a production deployment would handle it — the pattern scales from local dev to enterprise secret management (Vault, AWS Secrets Manager, etc.) without changing the application code.

---


---

## Mutual TLS Across the Polyglot Stack

Once the application was functional, the architecture diagram said "mTLS (internal)" but the implementation said "plain HTTP." Closing that gap across 6 language runtimes is the kind of cross-cutting work that's disproportionately expensive for a human working alone.

The approach: an init container (`cert-init`, Alpine + openssl) generates a CA and per-service certificates into a named Docker volume at startup. Every service waits for the init container to complete successfully before starting. The entire PKI is ephemeral — certificates have a 1-day TTL, the volume is destroyed when the stack comes down.

**What "mTLS across 6 languages" actually means:**

- **Go** (gateway, game-state, deck-service, bank-service, auth-ui-service): `tls.Config` with `RequireAndVerifyClientCert` on servers; `http.Transport` with `TLSClientConfig` on clients. One shared `mtlsTransport` initialized in `main()`.
- **Haskell** (hand-evaluator): `warp-tls` with `tlsWantClientCert = True` and `ServerHooks` for peer verification.
- **Python** (dealer-ai, email-service): Gunicorn flags `--certfile`, `--keyfile`, `--ca-certs`, `--cert-reqs 2`. Zero application code changes.
- **Elixir** (chat-service): `Plug.Cowboy` with `scheme: :https`, `verify: :verify_peer`, `fail_if_no_peer_cert: true`.
- **TypeScript** (auth-service): Node.js `https.createServer` with `requestCert: true`, `rejectUnauthorized: true`, CA pool from the shared volume.

A lesson learned the hard way: every HTTP client in every service needs the mTLS transport. Health handlers, dev reset endpoints, API helpers — anything that creates a plain HTTP client will fail against an mTLS endpoint. The fix is one shared client initialized before anything else starts.

Docker healthchecks cannot present client certificates, so internal mTLS services have their healthchecks removed. Startup ordering is handled by `depends_on: cert-init: condition: service_completed_successfully`.

---

## Contracts

Contracts were written before implementation from day one — the OpenAPI specs existed before Go was written, which is why everything composes cleanly. That's not what slipped.

What slipped was storage discipline. The `contracts/openapi/` directory held 5 files in a mix of YAML and markdown formats, well behind the actual number of services. The contracts existed in spirit; they just hadn't been maintained in the right place as the project evolved.

The fix: 12 OpenAPI 3.1 YAML files — one per service, written against the actual running implementation. The existing markdown files (email, observability, document) were kept as companion rationale documents; the YAML handles schema, the markdown handles why.

Drift is expected during development. The solution isn't preventing it — it's the periodic discipline of catching up. Writing contracts against the live implementation is a useful exercise regardless: it revealed several places where the bank service had gained endpoints (`/deposit`, `/withdraw`, `/export`) that were never formally documented, and forced the auth service token model — bootstrap JWT vs. session JWT, exchange code pattern — to be written down coherently for the first time.

## What Went Wrong (and How It Was Fixed)

**Honesty about failures is part of the case study.**

**Postfix on Alpine** — described above. Root cause was musl libc and Alpine's different syslog handling. Fix: Debian. Lesson: don't fight the base image.

**Dependency cycle** — `auth-service` → `email-service` → `auth-service` created a Docker Compose startup cycle. Root cause: email-service listed auth-service in `depends_on` for a runtime call that doesn't happen at startup. Fix: remove it from `depends_on`. Lesson: `depends_on` is about startup order, not runtime dependencies.

**Postfix chroot DNS** — Postfix runs in a chroot jail and can't see the container's `/etc/resolv.conf`. MX lookups failed silently. Fix: copy `resolv.conf` into `/var/spool/postfix/etc/` before starting. This is a well-documented Postfix behavior that doesn't manifest until you containerize it.

**Missing RFC 5322 Message-ID** — Gmail rejected emails missing a standard `Message-ID` header. The transport layer was setting a custom `X-Message-ID` but not the standard header Gmail requires. Fix: one line, format `<uuid@domain>`. Lesson: RFC compliance matters when talking to major mail providers.

**PTR record rejection** — direct MX delivery from a residential IP was rejected by Gmail because residential IPs don't have PTR records. This is a 20-year-old anti-spam measure. Fix: route through Gmail SMTP relay for local dev. Lesson: the architecture is correct for production (VPS with PTR record, or corporate mail relay); this is an infrastructure constraint, not a code problem.

**CA private key committed to git** — the CA key (`infra/certs/ca.key`) was committed to the repository in an early commit before proper `.gitignore` hygiene was in place. A CA private key in a repository is a meaningful exposure: anyone with repo access could sign certificates that would be trusted by all services in the stack.

The fix was immediate: `*.key`, `*.pem`, and `infra/certs/` were added to `.gitignore`. The key itself, being a self-signed development CA for a local PoC, carries limited real-world risk — but the correct response to any credential exposure is to treat it as compromised and rotate it, which the ephemeral cert-init design does on every `docker compose up`.

The more important fix was architectural: moving from static pre-generated certificates (`gen-certs.sh` → `infra/certs/`) to the init container pattern. Certificates now live in an ephemeral Docker volume, generated fresh at startup, destroyed on shutdown. There is nothing to commit because there is never a file on disk outside the container.

The lesson became a project guideline: **security scanning and a complete `.gitignore` are commit zero artifacts**, not things added after the fact. The scanning workflow commit message even says it: *"this process is going to be moved to the initial commit from now on."* That's the lesson being internalized in real time.

---

## The Productivity Question

This is the question every engineer and every employer will ask, so let's address it directly.

A complete polyglot microservices architecture — 12 services, 6 languages, full mutual TLS across the stack, WebAuthn passkey authentication, COBOL financial arithmetic, SSE, real-time observability, real email delivery, working game logic, 12 OpenAPI contracts, containerized with proper base images — was built in **under 40 hours of actual working time**, spread across approximately one calendar week.

The productivity gain is real. But the framing matters:

**What AI replaced:** typing, syntax lookup, boilerplate, cross-language translation, and the mechanical parts of implementation.

**What AI did not replace:** knowing what to build, knowing why, catching the things that don't work, understanding the security implications of each decision, recognizing when a proposed approach is wrong, and the accumulated experience that makes architectural judgment possible.

The engineer on this project has 20+ years of that judgment. Someone without it, handed the same AI tools, would not produce the same result. They would produce something that compiles and perhaps runs, but with the kinds of subtle flaws — security gaps, performance time bombs, unexamined coupling — that only surface under real conditions.

The correct framing: **AI amplifying expertise, not replacing engineers.** The leverage is real. It is not magic. It requires genuine engineering expertise to direct it effectively.

---

## What's Next

The project status is documented in `README.md`. The short version: core architecture is fully implemented and running. The foundation is solid and the path forward is clear.

The PoC is complete and fully functional as-is. Three things remain if this were to go further:

- **Multi-table support** — the architecture handles it; the UI shows one table. Adding more is a UI concern, not an architectural one.
- **Chat WebSocket upgrade** — the Elixir OTP actor model is already correct for WebSocket; only the transport changes.
- **OPA policy rules** — gateway scope enforcement is real and working. The `/policy/check` endpoint in auth-service is stubbed to always return `allowed: true`. Fine for a PoC; needs real Rego rules before production.

---

## Using This as a Reference

If you're evaluating this codebase: read the architecture diagram (`infra/swarm-architecture.html`), then pick any service and read its contract document. The contracts tell you what each service does and why. The implementations show how. The git history shows the progression.

If you have questions about specific decisions — why a particular language, why a particular security pattern, why something was done a certain way — those questions have real answers. Nothing here was chosen arbitrarily.

---

*Built with Claude (Anthropic) as AI partner. All architectural decisions, security design, and engineering judgment by Michael E. Shaffer.*
