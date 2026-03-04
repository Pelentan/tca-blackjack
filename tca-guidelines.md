# Tessellated Constellation Architecture — Project Guidelines

**Status:** Living Document  
**Last Updated:** 2026-03-04  
**Audience:** AI partners and engineers working on TCA projects  
**Scope:** Architectural rules, sequencing, and operational standards for any TCA implementation

---

## 1. What TCA Is (Operational Summary)

Tessellated Constellation Architecture is a polyglot microservices methodology built around three non-negotiable structural commitments:

1. **Contracts before code.** Every service interface is defined in a machine-verifiable OpenAPI 3.1 contract before a single line of implementation is written.
2. **Jobs as isolated units.** Each service (a "Job") does one thing, knows nothing about the outside world except what its contract tells it, and runs in its own container.
3. **Three-year lifecycle.** Every Job is designed to be completely rewritten within three years. This is not a failure state. It is the architecture working as intended.

AI is not incidental to TCA. It is structural. The reason contracts-first is enforceable now when it wasn't before is that an AI partner will honor a contract without drifting. The reason polyglot is practical now when it wasn't before is that no human needs to be an expert in every language in the stack. The reason three-year rewrites are feasible now is that a Job rewrite is an afternoon of work with an AI partner.

---

## 2. Project Initialization — Commit Zero

Before a single line of implementation code is written, the following must exist in the repository. These are checkboxes, not suggestions.

- [ ] **`.gitignore`** — generated for the full agreed polyglot stack. Covers compiled artifacts, dependency directories, IDE cruft, environment files, generated certificates, and any secret material paths defined by the project.
- [ ] **`.github/workflows/security.yml`** — CodeQL scanning configured for every language in the stack. If a language is added to the project later, the workflow is updated at that moment, not later.
- [ ] **GitHub secret scanning enabled.**
- [ ] **`README.md`** — minimum viable: what this is, architecture overview, stack with one-line rationale per component, how to run from a clean clone.

Security scanning at commit zero means every subsequent commit is scanned. Adding it later means every prior commit wasn't. There is no "we'll add it when things stabilize."

Certificates, API keys, and any generated credential material are excluded from the repository before those files are created. `.gitignore` first, secrets second.

---

## 3. Architecture-First Sequencing

Work proceeds in this order. Do not skip steps or compress them together.

**Step 1: Define the Jobs**  
Identify what each Job is responsible for — and only responsible for. A Job that is doing two distinct things is not a well-bounded Job. If a Job's description requires the word "and" to describe its primary function, that is a signal to split it.

**Step 2: Define stub behavior**  
Before writing contracts, decide which Jobs will be stubbed and what those stubs return. A stub is not an absence of design — it is a fully designed interface with deferred implementation. Stubs must return correct types and shapes, log visibly that they are stubs ("would send email to {address} via SMTP"), and have a single, clearly marked swap point where the real implementation replaces stub behavior. No caller should change when a stub is replaced.

**Step 3: Write the contracts**  
AI writes the contracts based on the agreed Job definitions. This is the first thing AI produces on a project, before any implementation code. See Section 4.

**Step 4: Implement**  
AI implements each Job against its contract. See Section 5.

The sequence is not flexible. A contract written after implementation is not a contract — it is documentation. Documentation drifts. Contracts don't.

---

## 4. Contracts

### The Rule
The contract is immutable after initial development is complete. If implementation friction suggests the contract should change, the answer is not to change the contract. Surface the conflict, re-examine the Job definition, and either adjust during the initial development window or create a new Job. Once callers exist, the contract is frozen.

This rule exists because the contract is what allows callers to be written without knowledge of the implementation. The moment a contract can be silently adjusted to resolve implementation friction, every caller becomes a liability. Do not propose contract changes as a resolution to implementation problems.

### Format
All TCA contracts are OpenAPI 3.1 YAML. Every contract must define:

- Every endpoint with its full path
- Every request body schema with required fields and types
- Every response schema for every status code, including error shapes
- The `servers` block referencing the service name (not a hardcoded IP or port)
- The `x-tca-observability` block (see Section 6)
- The `x-tca-security` block (see Section 7)

No other paths or response codes are implemented. If the contract doesn't define it, the Job doesn't return it.

### What the Contract Does Not Specify
The contract does not specify implementation language, internal data structures, internal logic, database technology, or anything else inside the Job boundary. The contract is the surface. The interior is entirely opaque to everyone outside the container.

### Contract Location
`contracts/openapi/{service-name}.yaml` in the project root.

---

## 5. Jobs

### Boundaries
A Job knows nothing about the outside world except what arrives through its contract-defined interface. It does not know the names of other services. It does not know how many instances of itself are running. It does not share a database with any other Job. It does not share code libraries with any other Job. Shared contracts and shared interfaces are fine. Shared state is not.

### Size
A Job should be at most a few thousand lines of implementation code. If it is substantially larger, it is likely doing more than one job. The practical test: a developer should be able to read the Job's code and understand what it does in a single sitting. More importantly, the entire Job must fit within an AI's context window. If it doesn't fit, it cannot be maintained, debugged, or rewritten with AI assistance — which defeats a core structural assumption of TCA.

### Language Selection
Language is chosen based on what is optimal for the Job's primary function. Not what the team knows. Not what the rest of the stack uses. The right language for the Job. AI removes the constraint that the team needs prior expertise — any modern language is viable if the AI can implement it competently and the engineer can read and verify the output.

If a language choice is causing persistent friction during implementation (library conflicts, version churn, poor fit for the problem domain), bring it back to the Job definition stage and reconsider. The Bank Job precedent: the contract required zero changes when the implementation language was replaced entirely. That is the standard.

### AI as Source of Truth for Code
During active development, the AI holds the authoritative state of the codebase. The engineer makes architectural decisions, validates the output, watches the build, and tests the running system. The AI tracks what was built, how, and what changed. Any local modifications made by the engineer must be fed back to the AI before the next build cycle.

This division is not about authority — the engineer has final authority on all decisions. It is about the AI maintaining coherent context across the full implementation so that changes are consistent and nothing gets lost between sessions.

---

## 6. Observability — `x-tca-observability`

Every Job contract must include the `x-tca-observability` extension block. Every Job implementation must be built with observability reporting baked in from the start — it is not added later.

Each Job reports all outbound calls to the Observability Job. The report is fire-and-forget: the Job does not wait for confirmation and the game is not affected if the Observability Job is unavailable.

**What gets reported:** caller service name, callee service name, HTTP method, path (sanitized), response status code, round-trip latency in milliseconds, protocol.

**What never gets reported:** user IDs in unsanitized form, JWT tokens, financial data, personally identifiable information.

The Observability Job owns all sanitization. Individual Jobs report raw data to Observability; Observability sanitizes before publishing to Redis pub/sub. No Job other than Observability publishes to the observability channel.

The `x-tca-observability` contract block signals to the AI implementing the Job that outbound call reporting is required. An AI encountering this block should implement the reporting wrapper for all outbound calls without being asked.

---

## 7. Security — `x-tca-security`

Every Job contract must include the `x-tca-security` extension block. Security posture is defined at the contract layer, not added during or after implementation.

### Zero Trust Posture
TCA targets zero-trust internal networking by default. Services trust the handshake, not the network.

**Service-to-service:** Mutual TLS on all internal service boundaries. Each service has its own certificate. Certificates are rotatable without contract changes. The internal network is not a trust boundary — mTLS is.

**User-to-service:** All user requests are authenticated and policy-checked at the gateway. JWT (short-lived, 15 minutes) plus Redis-backed refresh token. High-sensitivity operations (financial, account modification) re-validate the session independently at the service level — they do not rely solely on a valid JWT.

**Auth/OPA:** Policy decisions are centralized. Services ask "is this allowed?" rather than implementing their own authorization logic.

### The `x-tca-security` Block
The block in the contract defines:
- Minimum TLS version for this service (`1.3` preferred; `1.2` where library constraints force it — document the constraint)
- Whether the service is externally exposed or internal-only
- Authentication requirements for each endpoint
- Any service-specific policy requirements

An AI encountering the `x-tca-security` block implements the specified security posture without being asked. TLS version minimums, certificate configuration, and authentication requirements in the block are not suggestions — they are requirements.

### Stub Behavior for Security Services
During development, security services (Auth, OPA, Bank) may be stubbed to return "authorized" on all requests. The stub must:
- Accept the correct request shape
- Log visibly that it is returning a stub authorization ("STUB: auth check passed for {service} — real policy not enforced")
- Have a single swap point where real policy replaces the stub

Zero-trust structure is built in from commit zero even when the policy engine is stubbed. The structure is not added when the stub is replaced.

---

## 8. The Three-Year Lifecycle

Every Job is designed to be completely rewritten within three years. Design decisions should reflect this.

**What this means at implementation time:**
- No hard dependencies between Jobs beyond the contract interface
- No shared databases between services
- No shared code libraries between services
- All configuration external to the container (environment variables, not baked-in constants)
- Container images that build from source, not from a manually maintained state

**What this does not mean:**
- It does not mean building for throwaway quality. Jobs are built to production standards.
- It does not mean rewrites are scheduled. They happen when the landscape demands it: language versions, library churn, security currency, or a better implementation approach.
- It does not mean the contract changes. The contract outlives the implementation.

The practical test for whether a Job is well-bounded: can it be completely rewritten in an afternoon? If yes, the boundary is real. If no, something has leaked across it.

---

## 9. Kubernetes Deployment Patterns

### The Core Rule
Pods are the atomic unit in K8s — you cannot scale containers within a Pod independently. Never co-locate services in a single Pod to solve a latency problem. Use Pod Affinity to keep latency-sensitive services on the same node while keeping them in separate Pods.

### Service Grouping by Latency Budget

**Game Domain (or equivalent hot-path domain) — hard affinity, same node**  
Services in the critical request path. Affinity rules keep them on the same node; communication approaches loopback speeds without losing independent scaling.

**Security/Financial Domain — co-located with each other, isolated from hot path**  
Called less frequently, higher latency tolerance. Co-locate these with each other but don't pin them to hot-path nodes.

**Communication and async domains — float freely**  
Nearly decoupled from request latency. Let the scheduler place these on available capacity.

### The Legitimate Sidecar Exception
A true sidecar process — one that is always called by exactly one service and has no independent scaling requirement — may share a Pod. OPA as a policy engine co-located with Auth is the canonical example. This is the use case Pod co-location is designed for.

### Topology Spread Constraints
Affinity rules keep services close. Spread constraints keep them distributed across availability zones. Both are required. Affinity without spread is a single point of failure.

---

## 10. Dependency and Build Discipline

### Lockfiles Are Non-Negotiable
Every package manager produces a lockfile. Every lockfile is committed. Every Dockerfile uses the lockfile-respecting install command.

| Ecosystem | Lockfile | Dockerfile command |
|-----------|----------|--------------------|
| Node/npm | `package-lock.json` | `npm ci` |
| Go | `go.sum` | `go mod download` |
| Python | `requirements.txt` (pinned) | `pip install -r requirements.txt` |
| Java/Maven | `pom.xml` (explicit versions) | standard Maven with no version ranges |
| Rust | `Cargo.lock` | standard Cargo |

`npm install` in a Dockerfile without a committed lockfile produces version drift between local and container builds. The failure mode is builds that work locally and break in Docker, or worse, silently behave differently. Use `npm ci`.

### Dockerfile Layer Order
Dependencies before source. Cache invalidation on source changes should not re-run dependency installation.

```dockerfile
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
```

### No "Latest" Versions
"Latest" is not a version. All dependencies specify explicit versions. Unpinned dependencies are a supply chain risk and a reproducibility failure.

### Check Library API Versions Before Implementing
Training data trails reality. Before writing implementation code against any library with a major version history, verify the current API. The cost of one check is much lower than the cost of debugging version mismatch errors in a running container. This is especially critical for security-adjacent libraries (authentication, cryptography, TLS) where API changes often reflect security decisions.

---

## 11. Logging Standards

### Stdout Only
Services write to stdout and stderr. No file output inside containers. Where that output goes is an infrastructure concern, not an application concern — the application is fully decoupled from the logging destination.

### Structured JSON in Production
Development: plaintext to stdout is acceptable.  
Any environment feeding a log aggregator: structured JSON, one event per line.

Minimum fields:
```json
{
  "timestamp": "ISO 8601 UTC",
  "level": "info|warn|error",
  "service": "service-name",
  "message": "human-readable description",
  "request_id": "uuid"
}
```

Domain fields are added as top-level keys. No free-form string concatenation for fields that will be filtered or aggregated.

---

## 12. Service Exposure

Nothing is externally exposed that does not need to be.

In Docker Compose: only the gateway gets a published port. All other services communicate on the internal network only.

In Kubernetes: this maps to network policies. The mapping is 1:1 from Compose to K8s network policies by design.

The gateway is the single external entry point. TLS terminates there. JWT validation happens there. Everything downstream is internal.

---

## 13. What We've Learned (Running Log)

Most recent first. Add entries as lessons are established — not on every commit, but when a principle is proven or a painful mistake earns its place here.

**2026-03-04 — x-tca-security Belongs in the Contract, Not the Implementation**  
Security posture defined only in implementation code is invisible to the AI writing subsequent Jobs and invisible to the engineer reviewing contracts. The `x-tca-security` block in the contract makes security requirements a first-class part of the interface definition, not an afterthought.

**2026-02-26 — npm ci + Lockfiles Are Non-Negotiable in Docker**  
`npm install` in a Dockerfile without a committed lockfile produces version drift. Use `npm ci`. Same principle applies to every package manager.

**2026-02-26 — Check Library API Versions Before Writing Code**  
SimpleWebAuthn v10 had breaking changes from v9 that training data didn't reflect. For any library where major version history exists, verify the current API before implementation.

**2026-02-26 — Stdout Is the Only Correct Logging Target in Containers**  
Writing logs to files inside containers creates operational complexity with zero benefit. Stdout decouples the application from logging infrastructure entirely.

**2026-02-25 — Decomposition Is a Principle, Granularity Is a Variable**  
The right question is not "microservices or monolith?" It is: what is the latency budget, and where does it come from? Decompose so the latency-critical path contains only what it must. Everything else is a separate Job with its own contract.

**2026-02-25 — .gitignore and Security Actions Are Commit Zero**  
Security scanning added mid-project means every prior commit was unscanned. The `.gitignore` and security workflow are generated at project kickoff based on the agreed stack, before any code is written.

**2026-02-25 — Contracts Before Implementation**  
Writing OpenAPI specs first forces clarity on what each service actually does. Ambiguities that would have caused mid-implementation pivots get resolved at the design stage instead.

**2026-02-25 — Stub Contracts Are Real Contracts**  
A stubbed service has a fully defined request/response contract. When the real implementation replaces the stub, no caller changes. That is the test of a correctly designed stub.
