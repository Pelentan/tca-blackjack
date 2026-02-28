#!/bin/sh
PORT=${PORT:-3008}

if [ -n "$TLS_CERT" ] && [ -n "$TLS_KEY" ] && [ -n "$TLS_CA" ]; then
    echo "[email-service] starting on :${PORT} (mTLS)"
    exec gunicorn \
        --bind "0.0.0.0:${PORT}" \
        --workers 1 \
        --certfile "$TLS_CERT" \
        --keyfile  "$TLS_KEY" \
        --ca-certs "$TLS_CA" \
        --cert-reqs 2 \
        main:app
else
    echo "[email-service] starting on :${PORT} (plaintext — no TLS env vars)"
    exec gunicorn \
        --bind "0.0.0.0:${PORT}" \
        --workers 1 \
        main:app
fi
