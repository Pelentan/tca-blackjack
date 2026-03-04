# TCA Watchdog — Design Notes

**Status:** Future Iteration — Not Implemented  
**Last Updated:** 2026-03-04  
**Authors:** Michael E. Shaffer + Claude (Anthropic)  
**Warning:** This will require collaboration with security engineers who have forgotten more about this domain than either of us knows.

---

## What This Is

A design thinking document capturing a conversation about a new class of TCA Job — one that doesn't serve the application, but watches over the constellation itself. Not yet built. Not yet contracted. Here to preserve the thinking so it doesn't get lost.

---

## The Problem

TCA's contract-first architecture enforces participation through two mechanisms:

- **mTLS** — a Job without a CA-signed certificate cannot communicate with any other Job
- **x-tca-observability** — a Job is required by contract to report all outbound calls to the Observability Job

Both are contract obligations. But they're obligations that a bad actor — or a careless developer — can skip. A Job that skips the observability contract just doesn't show up in the event stream. A Job that skips mTLS sits inert on the network.

The question: can TCA detect a Job that shouldn't be there, or a legitimate Job that has been compromised into behaving incorrectly?

---

## The Threat Model That Drove This

The specific attack vector that crystallized the thinking:

**The Spider** — a rogue container that joins one or more constellation networks in promiscuous mode, captures raw packets, and exfiltrates them to an external endpoint for analysis.

Why this is dangerous even with mTLS:
- mTLS prevents a rogue service from *participating* in communication
- It does **not** prevent a rogue service from *listening*
- Network packet capture doesn't require completing a handshake
- The captured traffic is encrypted — but metadata (source, destination, timing, packet size, frequency) is visible immediately
- The encrypted payload can be stored for later decryption — harvest now, decrypt later is a real nation-state threat vector
- Cryptography that's unbreakable today may not be in 5 years

The Spider by behavioral signature:
- Never registers with the constellation
- Never appears in `tca:events` as a caller or callee
- Has no mTLS certificate
- Makes no outbound calls to any TCA service
- Just absorbs packets silently

---

## What the Watchdog Needs to Do

Detect the presence of containers on constellation networks that:

1. Have never registered as TCA Jobs
2. Are generating no observability events
3. Are not known infrastructure (databases, Redis, Postfix)

And alert immediately when found.

**What it does NOT need to do:**
- Understand what the rogue service is doing
- Decrypt or inspect traffic
- Automatically remediate (V1 — alert and partition is for humans to decide)
- Know in advance what services are supposed to be there

The dumber the better. Simple, fast, hard to subvert. A clever watchdog has more attack surface than a dumb one.

---

## The Discovery Problem

### Approach 1: Gateway as topology source
The gateway knows every service it routes to. It could publish a topology heartbeat to `tca:topology` on Redis. The watchdog cross-references against the observability stream.

**Problem:** The gateway only knows about services it routes to. It has no visibility into isolated networks:
- `financial-net` — bank-service ↔ bank-db
- `data-net` — game-state ↔ game-db
- `auth-net` internals — auth-service ↔ auth-db
- `mail-net` — email-service ↔ postfix

A Spider inserted between bank-service and bank-db would be completely invisible to the gateway. That's the highest-value target in the constellation.

**Verdict:** Partial solution only. Covers the routable surface, misses the isolated networks.

### Approach 2: Service self-registration
Every Job publishes a signed registration event at startup to `tca:registry`:

```json
{
  "service": "bank-service",
  "networks": ["financial-net", "redis-net"],
  "cert-fingerprint": "...",
  "timestamp": "...",
  "signed-by": "ca-cert"
}
```

The watchdog builds its constellation map from registrations, not from scanning. Cross-references against `tca:events` for behavioral compliance.

**Problem:** Only compliant Jobs register. A rogue service doesn't register. The watchdog knows about the constellation but still can't see what's on the network that *hasn't* announced itself.

**Verdict:** Solves the dynamic discovery problem for legitimate Jobs. Still can't detect silent rogues.

### Approach 3: Network scanning
The watchdog gets presence on every constellation network and does ARP/ping sweeps to detect unknown hosts.

**Problem:** IP addresses are meaningless in containerized environments. They're ephemeral, reassigned on restart, and in a scaling environment multiple instances of the same service have different IPs. IP-based detection is a losing game.

**Verdict:** Wrong primitive. Dead end.

### Approach 4: Privileged bridge + behavioral fingerprinting (most promising)

Split the problem into two components:

**The Bridge** — a small, privileged, heavily audited Job whose only responsibility is:
- Query the container runtime (Docker API / K8s API) for what containers exist on each network
- Translate that into TCA-native registry events: "these container IDs are on these networks"
- Sign those events with its own CA-signed certificate
- Publish to `tca:registry`

The Bridge is the **only** Job with runtime API access. Read-only. List containers/pods — nothing else. Everything else, including the watchdog, works from TCA-native Redis streams.

**The Watchdog** cross-references three streams:
- `tca:registry` (bridge) — what container IDs exist on each network
- `tca:events` (observability) — what is behaviorally active
- `tca:topology` (gateway) — what is routable

A container ID on a network that has never registered and generates no observability events — **that's the Spider.**

**Why the Bridge is the right abstraction:**
- The watchdog never touches the network directly
- The gateway remains the external trust boundary
- Infrastructure knowledge is translated into constellation-native signals once, at the bridge
- The bridge's job is so narrow it can be formally audited

**The Bridge is the highest-value target** — which means it needs:
- Minimal code, maximum auditability
- Read-only runtime API access only
- Independent cert rotation
- Dead man's switch — if the bridge goes silent, immediate alert
- Ideally: use an existing open-source runtime monitor (Falco for K8s) rather than building from scratch
- The constellation defines the contract the bridge publishes to; the security team picks the implementation

---

## The Dead Man's Switch Properties

Several alertable conditions fall out naturally from this architecture:

| Condition | Signal | Meaning |
|---|---|---|
| Container on network, no registration | Bridge reports it, no registry entry | Potential Spider |
| Registered Job, no observability events | Registry entry present, silent in events | Observability non-compliance or compromise |
| Bridge goes silent | `tca:registry` heartbeat stops | Bridge compromised or down — highest severity |
| Gateway topology goes silent | `tca:topology` heartbeat stops | Gateway issue |
| Known Job disappears from bridge | Was in registry, no longer reported | Container stopped or evicted |
| Unknown caller in events | Caller name not in observability allowlist | Dropped by observability-service, but watchdog catches it upstream |

---

## Known Infrastructure Exemptions

Databases, Redis, and Postfix are legitimate presences on constellation networks that will never register as TCA Jobs. The watchdog needs a small, explicitly maintained exemption list:

```yaml
expected-unregistered:
  - postgres      # game-db, bank-db, auth-db
  - redis         # session store, pub/sub
  - postfix       # SMTP relay
```

Everything else that appears on a network without registering triggers an alert.

This list is part of the watchdog's own contract — not hardcoded, not dynamic, explicitly maintained and version-controlled.

---

## What This Job Looks Like

**Language:** Go — fast, lightweight, scratch container, no runtime dependencies.

**Inputs:**
- `tca:registry` Redis subscription — container presence from the bridge
- `tca:events` Redis subscription — behavioral activity from observability
- `tca:topology` Redis subscription — routable services from gateway
- CA certificate — to verify all signed events

**Outputs:**
- `tca:alerts` Redis channel — security findings for consumption by gateway (UI panel) and/or external SIEM
- Structured JSON to stdout — feeds normal log infrastructure

**Runs:** Continuously. Wakes on Redis events, periodic sweep timer, never exits.

**Has no HTTP endpoints.** First TCA Job that is purely outbound — no API, no inbound connections, nothing to call. The contract for this Job looks fundamentally different from every other contract in the constellation.

**Does not appear in its own monitoring** — the watchdog is not in the observability service allowlist and does not report to the observability service. It operates outside the normal reporting chain by design.

---

## What Still Needs Thinking

1. **Automated partitioning vs. alert-only** — V1 should be alert-only. Automated network policy changes based on watchdog decisions is a significant responsibility that needs careful design and almost certainly human-in-the-loop for anything beyond the most obvious cases.

2. **The bridge implementation** — don't build this from scratch. Falco (K8s), Sysdig, or similar runtime security tools already solve the privileged observation problem. The work is defining the TCA contract they publish to, not writing a new runtime monitor.

3. **`tca:alerts` consumers** — the gateway surfaces alerts in the UI for demo purposes. Production needs a defined contract for the alert shape so a SIEM can consume it directly. Same pattern as `tca:events` → `/events` SSE endpoint.

4. **The watchdog's own cert** — should it use the same CA as the constellation, or an independent CA? Arguments for independent: compromise of the constellation CA doesn't compromise the watchdog. Arguments against: operational complexity. Unresolved.

5. **Scaling** — if the constellation scales horizontally, multiple instances of the same service appear on the bridge's radar. The watchdog needs to understand that "three instances of game-state" is not three unknown containers. The registration contract needs to account for instance identity vs. service identity.

6. **False positive management** — during deployments, containers appear and disappear legitimately. The watchdog needs to distinguish "new deployment" from "Spider." Probably a grace period + deployment event signal, but the details matter.

---

## The Architectural Statement

The Watchdog is the one Job in the constellation with deliberate cross-network visibility — granted explicitly through the Bridge, scoped minimally, for the sole purpose of detecting what shouldn't be there.

Every other Job sees only its own network. The Watchdog sees all networks but talks to none of them directly.

That's not a security hole. That's a watchdog with a window.

---

## Why This Is a Future Iteration

This isn't complex because the code is hard. It's complex because the threat model requires people who think like attackers professionally — red team experience, container runtime security expertise, network forensics background. The design above is architecturally sound but the implementation details — particularly around the bridge, false positive rates, and automated response — need security engineering expertise that goes beyond what was brought to TCA Blackjack.

Get the security team salivating on the white paper first. Then invite them to design this with you.

---

*Captured from design conversation, 2026-03-04. Both participants understood this was thinking-out-loud, not specification. Treat accordingly.*
