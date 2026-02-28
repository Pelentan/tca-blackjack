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

## What Went Wrong (and How It Was Fixed)

**Honesty about failures is part of the case study.**

**Postfix on Alpine** — described above. Root cause was musl libc and Alpine's different syslog handling. Fix: Debian. Lesson: don't fight the base image.

**Dependency cycle** — `auth-service` → `email-service` → `auth-service` created a Docker Compose startup cycle. Root cause: email-service listed auth-service in `depends_on` for a runtime call that doesn't happen at startup. Fix: remove it from `depends_on`. Lesson: `depends_on` is about startup order, not runtime dependencies.

**Postfix chroot DNS** — Postfix runs in a chroot jail and can't see the container's `/etc/resolv.conf`. MX lookups failed silently. Fix: copy `resolv.conf` into `/var/spool/postfix/etc/` before starting. This is a well-documented Postfix behavior that doesn't manifest until you containerize it.

**Missing RFC 5322 Message-ID** — Gmail rejected emails missing a standard `Message-ID` header. The transport layer was setting a custom `X-Message-ID` but not the standard header Gmail requires. Fix: one line, format `<uuid@domain>`. Lesson: RFC compliance matters when talking to major mail providers.

**PTR record rejection** — direct MX delivery from a residential IP was rejected by Gmail because residential IPs don't have PTR records. This is a 20-year-old anti-spam measure. Fix: route through Gmail SMTP relay for local dev. Lesson: the architecture is correct for production (VPS with PTR record, or corporate mail relay); this is an infrastructure constraint, not a code problem.

---

## The Productivity Question

This is the question every engineer and every employer will ask, so let's address it directly.

A complete polyglot microservices architecture — 9 services, 6 languages, SSE, observability, zero trust security design, real email delivery, working game logic, containerized with proper base images — was built in sessions measured in hours, not weeks.

The productivity gain is real. But the framing matters:

**What AI replaced:** typing, syntax lookup, boilerplate, cross-language translation, and the mechanical parts of implementation.

**What AI did not replace:** knowing what to build, knowing why, catching the things that don't work, understanding the security implications of each decision, recognizing when a proposed approach is wrong, and the accumulated experience that makes architectural judgment possible.

The engineer on this project has 20+ years of that judgment. Someone without it, handed the same AI tools, would not produce the same result. They would produce something that compiles and perhaps runs, but with the kinds of subtle flaws — security gaps, performance time bombs, unexamined coupling — that only surface under real conditions.

The correct framing: **AI amplifying expertise, not replacing engineers.** The leverage is real. It is not magic. It requires genuine engineering expertise to direct it effectively.

---

## What's Next

The project status is documented in `README.md`. The short version: core architecture is proven, several services still have stub implementations that need promotion to full functionality. The foundation is solid and the path forward is clear.

For production deployment:
- OPA policy rules (currently allow-all)
- mTLS enforcement (certs generated, not yet enforced per-service)
- Real passkey/WebAuthn ceremony (auth service currently uses simplified demo flow)
- PostgreSQL schemas for game history
- Multi-table support (architecture supports it, UI shows one)
- Chat UI integration (Elixir service running, not yet surfaced in UI)

---

## Using This as a Reference

If you're evaluating this codebase: read the architecture diagram (`infra/swarm-architecture.html`), then pick any service and read its contract document. The contracts tell you what each service does and why. The implementations show how. The git history shows the progression.

If you have questions about specific decisions — why a particular language, why a particular security pattern, why something was done a certain way — those questions have real answers. Nothing here was chosen arbitrarily.

---

*Built with Claude (Anthropic) as AI partner. All architectural decisions, security design, and engineering judgment by Michael E. Shaffer.*
