#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# cert-init — Generate mTLS certificates at stack startup
#
# Runs once as an init container. Generates a fresh CA and per-service certs
# into /certs (a Docker named volume shared with all services).
#
# Key material lives only in the ephemeral volume — never on the host
# filesystem, never in an image layer. Destroyed on `docker compose down`.
#
# In production: replace with cert-manager (K8s) or Vault PKI.
# This pattern gives equivalent security properties for a single-host PoC:
#   - Ephemeral keys, fresh per stack startup
#   - Verified mutual TLS between all services
#   - Zero manual steps, zero committed secrets
# ─────────────────────────────────────────────────────────────────────────────

set -euo pipefail

CERTS_DIR="/certs"

SERVICES=(
  "gateway"
  "game-state"
  "deck-service"
  "hand-evaluator"
  "dealer-ai"
  "bank-service"
  "auth-service"
  "auth-ui-service"
  "chat-service"
  "email-service"
  "document-service"
  "observability-service"
)

# If certs already exist from a previous run in the same volume lifecycle,
# skip regeneration. Volume is destroyed on `docker compose down` so this
# only matters for `docker compose restart` within the same session.
if [ -f "$CERTS_DIR/ca.crt" ]; then
  echo "✓ Certificates already present — skipping generation"
  exit 0
fi

echo "🔐 cert-init: Generating ephemeral mTLS certificates"
echo "   Certs live in Docker volume only — destroyed on compose down"
echo ""

# ── Certificate Authority ─────────────────────────────────────────────────────
echo "→ Creating Swarm CA..."
openssl req -x509 -newkey rsa:4096 -days 1 -nodes \
  -keyout "$CERTS_DIR/ca.key" \
  -out    "$CERTS_DIR/ca.crt" \
  -subj   "/C=US/O=Swarm Blackjack/CN=Swarm Ephemeral CA" \
  2>/dev/null

echo "  ✓ CA certificate (1-day TTL — ephemeral by design)"
echo ""

# ── Per-Service Certificates ──────────────────────────────────────────────────
for SERVICE in "${SERVICES[@]}"; do
  echo "→ $SERVICE..."

  openssl req -newkey rsa:2048 -nodes \
    -keyout "$CERTS_DIR/$SERVICE.key" \
    -out    "$CERTS_DIR/$SERVICE.csr" \
    -subj   "/C=US/O=Swarm Blackjack/CN=$SERVICE" \
    2>/dev/null

  cat > "$CERTS_DIR/$SERVICE.ext" << EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage=digitalSignature, keyEncipherment
extendedKeyUsage=serverAuth, clientAuth
subjectAltName=DNS:$SERVICE,DNS:localhost,IP:127.0.0.1
EOF

  openssl x509 -req -days 1 \
    -in      "$CERTS_DIR/$SERVICE.csr" \
    -CA      "$CERTS_DIR/ca.crt" \
    -CAkey   "$CERTS_DIR/ca.key" \
    -CAcreateserial \
    -out     "$CERTS_DIR/$SERVICE.crt" \
    -extfile "$CERTS_DIR/$SERVICE.ext" \
    2>/dev/null

  rm "$CERTS_DIR/$SERVICE.csr" "$CERTS_DIR/$SERVICE.ext"
  echo "  ✓ $SERVICE.{key,crt}"
done

# World-readable — ephemeral volume, services may run as non-root users
chmod 644 "$CERTS_DIR"/*.key
chmod 644 "$CERTS_DIR"/*.crt

echo ""
echo "✅ cert-init complete — $(ls "$CERTS_DIR"/*.crt | wc -l) certificates issued"
echo "   Stack is ready to start."
