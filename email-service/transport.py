"""
transport.py — Email Transport Layer
=====================================
This is the ONLY file that knows about SMTP (or any delivery mechanism).
Everything above this layer is transport-agnostic.

Configuration (environment variables):
  SMTP_HOST      — SMTP server hostname (default: mailhog)
  SMTP_PORT      — SMTP port (default: 1025 for MailHog, 587 for real SMTP)
  SMTP_USER      — Username for SMTP auth (optional, leave empty for MailHog)
  SMTP_PASSWORD  — Password for SMTP auth (optional, leave empty for MailHog)
  SMTP_FROM      — From address (default: noreply@tca-blackjack.local)
  SMTP_USE_TLS   — Use STARTTLS (default: false, set true for real SMTP)

For dev: SMTP_HOST=mailhog SMTP_PORT=1025, no auth, MailHog catches everything.
For prod: point at real SMTP relay, set credentials.
"""

import os
import logging
import smtplib
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
from dataclasses import dataclass

log = logging.getLogger(__name__)

# ── SMTP Configuration ────────────────────────────────────────────────────────

SMTP_HOST    = os.environ.get('SMTP_HOST', 'mailhog')
SMTP_PORT    = int(os.environ.get('SMTP_PORT', '1025'))
SMTP_USER    = os.environ.get('SMTP_USER', '')
SMTP_PASSWORD = os.environ.get('SMTP_PASSWORD', '')
SMTP_FROM    = os.environ.get('SMTP_FROM', 'noreply@tca-blackjack.local')
SMTP_USE_TLS = os.environ.get('SMTP_USE_TLS', 'false').lower() == 'true'


@dataclass
class TransportMessage:
    """Normalized message envelope. Transport layer speaks only this."""
    to_address: str
    subject: str
    body_text: str
    body_html: str | None
    message_id: str
    encrypted: bool
    tier: str


@dataclass
class TransportResult:
    success: bool
    error: str | None = None


def _build_mime(msg: TransportMessage) -> MIMEMultipart:
    """Build MIME message with text and optional HTML parts."""
    mime = MIMEMultipart('alternative')
    mime['Subject'] = msg.subject
    mime['From']    = SMTP_FROM
    mime['To']      = msg.to_address
    mime['Message-ID']  = f"<{msg.message_id}@{SMTP_FROM.split('@')[-1]}>"
    mime['X-Message-ID']    = msg.message_id
    mime['X-TCA-Tier']    = msg.tier
    mime['X-TCA-Encrypted'] = str(msg.encrypted)

    mime.attach(MIMEText(msg.body_text, 'plain', 'utf-8'))
    if msg.body_html:
        mime.attach(MIMEText(msg.body_html, 'html', 'utf-8'))

    return mime


def _smtp_send(msg: TransportMessage) -> TransportResult:
    """
    Real SMTP delivery via smtplib.
    Falls back to console log if SMTP_HOST is not configured.
    """
    if not SMTP_HOST:
        log.warning(f"[{msg.message_id}] SMTP_HOST not set — falling back to console output")
        _console_fallback(msg)
        return TransportResult(success=True)

    mime = _build_mime(msg)

    try:
        log.info(f"[{msg.message_id}] Connecting to SMTP {SMTP_HOST}:{SMTP_PORT}")
        with smtplib.SMTP(SMTP_HOST, SMTP_PORT, timeout=10) as server:
            if SMTP_USE_TLS:
                server.starttls()
                log.info(f"[{msg.message_id}] STARTTLS enabled")
            if SMTP_USER and SMTP_PASSWORD:
                server.login(SMTP_USER, SMTP_PASSWORD)
                log.info(f"[{msg.message_id}] Authenticated as {SMTP_USER}")

            server.sendmail(SMTP_FROM, [msg.to_address], mime.as_string())

        log.info(f"[{msg.message_id}] Delivered: to={msg.to_address} subject='{msg.subject}' "
                 f"tier={msg.tier} encrypted={msg.encrypted}")
        return TransportResult(success=True)

    except smtplib.SMTPException as e:
        log.error(f"[{msg.message_id}] SMTP error: {e}")
        return TransportResult(success=False, error=f"SMTP error: {e}")
    except OSError as e:
        log.error(f"[{msg.message_id}] Connection failed to {SMTP_HOST}:{SMTP_PORT}: {e}")
        return TransportResult(success=False, error=f"Connection failed: {e}")


def _console_fallback(msg: TransportMessage) -> None:
    """Last-resort logging when SMTP is unavailable."""
    log.info("=" * 60)
    log.info("EMAIL (console fallback — no SMTP configured)")
    log.info(f"  message_id : {msg.message_id}")
    log.info(f"  tier       : {msg.tier}")
    log.info(f"  encrypted  : {msg.encrypted}")
    log.info(f"  to         : {msg.to_address}")
    log.info(f"  subject    : {msg.subject}")
    log.info(f"  body       : {msg.body_text[:300]}{'...' if len(msg.body_text) > 300 else ''}")
    log.info("=" * 60)


def deliver(msg: TransportMessage) -> TransportResult:
    """Public interface. Pipeline calls this — never calls _smtp_send directly."""
    try:
        return _smtp_send(msg)
    except Exception as e:
        log.error(f"Transport error for {msg.message_id}: {e}")
        return TransportResult(success=False, error=str(e))


def smtp_config_summary() -> dict:
    """Return current SMTP config for health endpoint."""
    return {
        "host":    SMTP_HOST or "(not set — console fallback)",
        "port":    SMTP_PORT,
        "from":    SMTP_FROM,
        "auth":    bool(SMTP_USER),
        "tls":     SMTP_USE_TLS,
        "mode":    "mailhog" if SMTP_HOST == "mailhog" else ("smtp" if SMTP_HOST else "console"),
    }
