#!/bin/sh
# Start gunicorn with mTLS if cert env vars are present, plain HTTP otherwise.

PORT=${PORT:-3004}

if [ -n "$TLS_CERT" ] && [ -n "$TLS_KEY" ] && [ -n "$TLS_CA" ]; then
    echo "[dealer-ai] starting on :${PORT} (mTLS)"
    exec gunicorn \
        --bind "0.0.0.0:${PORT}" \
        --workers 2 \
        --certfile "$TLS_CERT" \
        --keyfile  "$TLS_KEY" \
        --ca-certs "$TLS_CA" \
        --cert-reqs 2 \
        main:app
else
    echo "[dealer-ai] starting on :${PORT} (plaintext — no TLS env vars)"
    exec gunicorn \
        --bind "0.0.0.0:${PORT}" \
        --workers 2 \
        main:app
fi
