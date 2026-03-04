# Engineer-AI Partnership — Project Guidelines

**Status:** Living Document  
**Last Updated:** 2026-02-28  
**Audience:** Michael + Claude (working reference)

---

## 1. Project Kickoff Checklist

Before any code is written, the following must exist. Non-negotiable.

### Stack Definition First
Agree on the full tech stack before touching a file. Language choices, framework choices, database choices. These decisions drive everything that follows — .gitignore, security scanning, container base images, CI/CD pipeline.

### Git Initialization (Commit Zero)
Every project's first commit must include, at minimum:

**`.gitignore`** — generated for the agreed stack. Polyglot projects get a combined file. Covers:
- Compiled artifacts and binaries (language-specific)
- Dependency directories (`node_modules/`, `vendor/`, `__pycache__/`, etc.)
- IDE/editor cruft (`.idea/`, `.vscode/`, `*.swp`)
- Environment files (`.env`, `.env.local`, `*.env`)
- OS junk (`.DS_Store`, `Thumbs.db`)
- Docker-specific (override files, local certs if generated)
- Any secrets or key material paths defined in the project

**`.github/workflows/security.yml`** — CodeQL scanning configured for the stack. If GitHub Actions isn't the CI, adapt accordingly, but security scanning is mandatory and goes in from day one.

For polyglot projects, CodeQL config must enumerate all languages present. Don't let this be discovered on push.

---

## 2. Architecture-First Sequencing

We design before we build. In this order:

1. **Define service boundaries** — what is each service responsible for, and only responsible for
2. **Write service contracts** (OpenAPI YAML or equivalent) — before implementation begins
3. **Define stub behavior** — every stubbed service has a documented swap point (see Section 4)
4. **Then implement**

We learned this correctly in TCA Blackjack. Game state YAML, gateway YAML, and deck service YAML existed before a line of Go was written. The email and observability contracts were designed before those services were built. This is the pattern.

---

## 3. Three-Year Lifecycle as an Architectural Input

Every service is designed to be completely rewritten within its first three years. This is not a failure state — it's the target.

This principle shapes decisions at design time:

- **Hard service boundaries** — no shared databases between services, no shared code libraries between services (shared contracts/interfaces are fine)
- **REST over direct coupling** — services communicate via documented HTTP contracts, not shared state or in-process calls
- **No "temporary" architectural decisions** — if it goes in, it's a design choice we can explain
- **Containers as isolation units** — each service is independently deployable and independently replaceable

The payoff: a service rewrite is a bounded, contained event. It doesn't require coordinating with five other teams or migrating a shared schema.

---

## 4. Stub Discipline

Stubs are not placeholders — they are designed interfaces with deferred implementation.

Every stub must have:
- A fully defined request/response contract (same as a real service)
- A clearly marked swap point (the single function or class where real implementation replaces stub behavior)
- Console/log output that makes stubbing visible during demo ("would send email to X via SMTP")
- Zero upstream impact when the stub is replaced — callers must not need to change

**What we do not do:** stub the interface too. The contract is real even when the implementation isn't.

---

## 5. Security Posture

### Sensitive Data in Git
Environment files never go in. If a file could contain a secret, it's in `.gitignore` before the file is created. Generated certs, API keys, database credentials — all excluded.

### Service Exposure
Nothing is externally exposed that doesn't need to be. In Docker Compose, only the gateway gets a published port. All other services are on the internal network only. This maps directly to K8s network policies when we migrate.

### Dependency Hygiene
No dependency goes in without a reason. For Go: explicit `go.mod` with pinned versions. For Node: `package-lock.json` committed. For Python: `requirements.txt` with pinned versions. "Latest" is not a version.

---

## 6. README Standard

Every project gets a README at commit zero. Minimum sections:

- **What this is** — one paragraph, no jargon
- **Architecture overview** — diagram or table of services/components
- **Stack** — language and framework per component with a one-line rationale
- **How to run** — must work from a clean clone with documented prerequisites
- **AI-Augmented Development** — see template below

### AI-Augmented Development Section Template
```markdown
## Development Methodology

This project was built through AI-augmented development — a human-AI partnership 
where architectural decisions, security design, and engineering judgment came from 
human expertise, and implementation was accelerated by an AI collaborator fluent 
across the full stack.

The goal: demonstrate that experienced engineers working with AI can achieve 
significant productivity gains while maintaining (and in some cases exceeding) 
the quality standards of traditional development.
```

Adjust specifics per project, but the transparency is always there.

---

## 7. The "Measure Twice" Rule

Before building anything non-trivial, we pause and ask:

- What are the edge cases?
- Are there decisions in this implementation that will be painful to reverse?
- Is the requirement actually clear, or are we assuming?

This feels slower. It isn't. Getting it right the first time beats two rounds of fixes.

Trivial tasks (fixing a typo, renaming a variable) skip this. The judgment call on what's "trivial" is Michael's.

---

## 8. Delivery Standards (Code)

When delivering code changes:

- Every tarball includes `CHANGED_FILES.md` with date (UTC), feature description, and lists of modified and added files
- File paths are relative from project root
- No wrapper folders — extract directly into project directory
- Only changed/new files — not the whole project

**Beta quality bar:** zero manual steps after `docker compose build`. If it requires running a SQL command or editing a config file by hand, it's not done.

**Delivery command (always):**
```bash
tar -xzf <filename>.tar.gz --overwrite
docker compose build --no-cache <service(s)>
```
No path argument on the tar — assume project root. List only the affected services on the build command.

---

## 9. What We've Learned (Running Log)

Lessons added as we discover them. Most recent first.

**2026-02-25 — .gitignore + Security Actions are Commit Zero**  
We discovered this reactively in the local AI machine conversation when the question "what should be in my .gitignore?" came up right before pushing to GitHub. That question should never happen mid-project. Both files are generated at project kickoff based on the agreed stack, before any code is written.

**2026-02-25 — Service Contracts Before Implementation**  
Validated in TCA Blackjack: writing the OpenAPI specs first forced clarity on what each service actually does. Ambiguities that would have caused mid-implementation pivots got resolved at the design stage instead.

**2026-02-25 — Stub Contracts Are Real Contracts**  
The email service being "stubbed" doesn't mean its interface is informal. The full request/response contract was designed and documented. When real SMTP goes in, no caller changes. That's the test of a good stub.
**2026-02-28 — mTLS Across a Polyglot Stack: Implementation Patterns**  
Mutual TLS across six languages (Go, Haskell, Python/Flask, Python/Gunicorn, Elixir, COBOL-via-Go) each has one idiomatic entry point. Go: `tls.Config` with `RequireAndVerifyClientCert` on server, `http.Transport` with `TLSClientConfig` on client. Haskell/warp-tls: `tlsSettings` with `tlsWantClientCert = True` and `ServerHooks`. Python/Gunicorn: `--certfile`, `--keyfile`, `--ca-certs`, `--cert-reqs 2` flags — zero code changes. Elixir/Plug.Cowboy: `scheme: :https` with `verify: :verify_peer, fail_if_no_peer_cert: true`. The key insight: mTLS is an infrastructure concern in most frameworks, not an application concern. You configure it at the server/transport level and the application code is untouched.

**2026-02-28 — Init Container Pattern for Ephemeral PKI**  
External cert generation (manual `gen-certs.sh`) is the right call for production PKI — CA key never lives in containers. For single-host PoC it's pure friction. Init container pattern: Alpine with openssl, generates CA + per-service certs into a named Docker volume on startup, `restart: "no"` so it exits cleanly, all downstream services `depends_on` with `condition: service_completed_successfully`. Result: zero manual steps, ephemeral 1-day CA, volume destroyed on `docker compose down`. Demo story: "In production this is cert-manager. Here it's the same security properties — ephemeral keys, verified mutual TLS — without the operational overhead."

**2026-02-28 — Docker Volumes Survive `docker compose down`**  
`docker compose down` removes containers, not volumes. If certs are stale or a volume needs to be regenerated, the command is `docker compose down && docker volume rm <project>_<volume-name>`. This caused repeated "bad certificate" failures when services were brought up across multiple compose cycles with a stale cert volume. The fix: always include the volume rm when cert rotation is the goal.

**2026-02-28 — Every HTTP Client in a Service Needs the mTLS Transport**  
When wiring mTLS into an existing service, `grep` for every `http.Client`, `http.Get`, `http.Post`, `http.DefaultClient` — not just the main proxy path. Health handlers, dev/reset endpoints, and direct API call helpers all create their own clients and will send plain HTTP to mTLS-only listeners. Go pattern: one shared `mtlsClient` or `mtlsTransport` var, initialized once in `main()` before anything else runs, referenced everywhere. Any client created with `&http.Client{}` after that point is a bug.

**2026-02-28 — Healthchecks Can't Present Client Certs**  
Docker healthchecks (`wget`, `curl`) have no mechanism to present a client certificate. Any internal service running mTLS must have its healthcheck removed — startup ordering is handled by `depends_on: cert-init: condition: service_completed_successfully`, not Docker health state. Gateway and externally-facing services (plain HTTP listener) keep their healthchecks. The error signature when a healthcheck hits an mTLS port: `tls: client didn't provide a certificate` (Go) or `{:unsupported_record_type, 71}` (Elixir — `0x47` = `G`, first byte of `GET`).

**2026-02-28 — "Bad Certificate" vs "No Certificate" Are Different Failures**  
`tls: client didn't provide a certificate` — client is speaking TLS but not sending a cert. Healthcheck or wrong client config. `tls: bad certificate` / `SSLV3_ALERT_BAD_CERTIFICATE` — client IS presenting a cert but the server can't verify it against its CA pool. Stale volume (certs from a previous CA), or a service calling upstream with plain `http.Client{}` instead of the mTLS transport. Mixed cert generations from partial restarts cause the second error; `docker volume rm` is the fix.

**2026-03-04 — Database Connections Are Not mTLS — Production Deployment Note**  
In TCA Blackjack, service-to-service communication is secured with mutual TLS via TCA CA-issued certificates. Database connections (PostgreSQL, Redis) are currently protected by network isolation only — each database is on an isolated Docker network accessible only to its authorized service. This is acceptable for a single-host PoC. Production requires TLS on database connections, but the scope of that work is deliberately outside TCA's boundaries for two reasons: (1) PostgreSQL and Redis are not TCA Jobs — they don't participate in the mutual authentication ceremony and would require server-side TLS configuration, not TCA CA certificates; (2) in any real deployment, the high-value databases (bank, player) would almost certainly be external to the application entirely — managed services (RDS, Azure Database) or existing enterprise infrastructure with their own DBAs, security policies, and network controls. TLS on those connections is a "here's our connection string, talk to your DBA" conversation, not a TCA architecture concern. TCA defines the security boundaries of its own Jobs. What lives outside those boundaries is integrated at the contract level and owned by whoever owns that infrastructure.
