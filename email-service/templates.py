"""
templates.py — Message Type Registry
======================================
Defines all valid message types, their minimum tier, required payload fields,
and renders them to subject/body strings.

render() returns (subject, body_text, body_html | None).
HTML is only provided where it meaningfully improves readability (verify_email,
magic_link). Plain text is always provided for non-HTML clients and encryption.

Adding a new message type: add entry to MESSAGE_TYPES and a render function.
Nothing else needs to change.
"""

from dataclasses import dataclass


@dataclass
class MessageTypeSpec:
    minimum_tier:    str
    required_fields: list[str]
    description:     str


# ── Registry ──────────────────────────────────────────────────────────────────

MESSAGE_TYPES: dict[str, MessageTypeSpec] = {

    # System tier
    "verify_email": MessageTypeSpec(
        minimum_tier="system",
        required_fields=["verification_url", "expires_in"],
        description="New account email verification link",
    ),
    "magic_link": MessageTypeSpec(
        minimum_tier="system",
        required_fields=["magic_url", "expires_in"],
        description="One-time account setup link",
    ),

    # Honeypot — kept in registry so it passes schema validation.
    # Auth layer handles detection and rejection before we get to rendering.
    "password_reset": MessageTypeSpec(
        minimum_tier="system",
        required_fields=["reset_url", "expires_in"],
        description="[HONEYPOT] Password reset — not a valid operation in this system",
    ),

    # Social tier
    "game_invite": MessageTypeSpec(
        minimum_tier="social",
        required_fields=["inviter_name", "table_name", "join_url"],
        description="Player-to-player game invitation",
    ),
    "game_result_notify": MessageTypeSpec(
        minimum_tier="social",
        required_fields=["result", "net_change"],
        description="Notification of game result to interested party",
    ),

    # Personal tier
    "session_summary": MessageTypeSpec(
        minimum_tier="personal",
        required_fields=["hands_played", "net_result", "session_start", "session_end"],
        description="Player's own session win/loss summary",
    ),

    # Confidential tier
    "account_flag_notice": MessageTypeSpec(
        minimum_tier="confidential",
        required_fields=["reason", "action_taken"],
        description="Account moderation or flag notification",
    ),

    # Restricted tier
    "transaction_receipt": MessageTypeSpec(
        minimum_tier="restricted",
        required_fields=["transaction_id", "amount", "type", "timestamp", "balance_after"],
        description="Bank transaction receipt — always encrypted, always to registered address",
    ),
}

TIER_LEVELS = {
    "system": 1, "social": 2, "personal": 3, "confidential": 4, "restricted": 5
}


# ── Validation ────────────────────────────────────────────────────────────────

def validate(message_type: str, tier: str, payload: dict) -> list[str]:
    """Returns list of validation errors. Empty list = valid."""
    errors = []
    spec = MESSAGE_TYPES.get(message_type)
    if not spec:
        errors.append(f"Unknown message type: {message_type}")
        return errors
    if TIER_LEVELS.get(tier, 0) < TIER_LEVELS.get(spec.minimum_tier, 0):
        errors.append(
            f"Message type '{message_type}' requires minimum tier "
            f"'{spec.minimum_tier}', got '{tier}'"
        )
    for field in spec.required_fields:
        if field not in payload or payload[field] is None:
            errors.append(f"Missing required payload field: '{field}'")
    return errors


# ── Renderers ─────────────────────────────────────────────────────────────────

def render(message_type: str, payload: dict, encrypted: bool) -> tuple[str, str, str | None]:
    """
    Returns (subject, body_text, body_html | None).
    body_html is None for most message types — only provided where HTML adds value.
    Raises KeyError if message_type not in registry (validate first).
    """
    enc_notice = "\n[This message is encrypted end-to-end]\n" if encrypted else ""
    renderers = {
        "verify_email":        _render_verify_email,
        "magic_link":          _render_magic_link,
        "password_reset":      _render_password_reset,
        "game_invite":         _render_game_invite,
        "game_result_notify":  _render_game_result_notify,
        "session_summary":     _render_session_summary,
        "account_flag_notice": _render_account_flag_notice,
        "transaction_receipt": _render_transaction_receipt,
    }
    subject, body_text, body_html = renderers[message_type](payload)
    return subject, enc_notice + body_text, body_html


# ── HTML base template ────────────────────────────────────────────────────────

def _html_wrap(title: str, body_inner: str) -> str:
    return f"""<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{title}</title>
</head>
<body style="margin:0;padding:0;background:#0d1117;font-family:'Segoe UI',system-ui,sans-serif;">
  <table width="100%" cellpadding="0" cellspacing="0" style="background:#0d1117;padding:40px 0;">
    <tr><td align="center">
      <table width="480" cellpadding="0" cellspacing="0"
             style="background:#161b22;border:1px solid #30363d;border-radius:12px;overflow:hidden;">
        <!-- Header -->
        <tr>
          <td style="background:linear-gradient(135deg,#1a4731,#0d2818);
                     padding:28px 32px;text-align:center;border-bottom:1px solid #30363d;">
            <div style="color:#58a6ff;font-size:22px;font-weight:700;
                        letter-spacing:3px;text-transform:uppercase;">
              TCA Blackjack
            </div>
            <div style="color:#8b949e;font-size:12px;margin-top:4px;letter-spacing:1px;">
              Polyglot Microservices · Zero Trust · PoC
            </div>
          </td>
        </tr>
        <!-- Body -->
        <tr>
          <td style="padding:32px;">
            {body_inner}
          </td>
        </tr>
        <!-- Footer -->
        <tr>
          <td style="padding:16px 32px 24px;border-top:1px solid #21262d;text-align:center;">
            <div style="color:#4a5568;font-size:11px;">
              This is an automated message from TCA Blackjack.
              If you didn't request this, you can safely ignore it.
            </div>
          </td>
        </tr>
      </table>
    </td></tr>
  </table>
</body>
</html>"""


# ── Individual renderers ──────────────────────────────────────────────────────

def _render_verify_email(p: dict) -> tuple[str, str, str | None]:
    url = p['verification_url']
    expires = p['expires_in']
    text = (
        f"Please verify your email address by clicking the link below.\n\n"
        f"{url}\n\n"
        f"This link expires in {expires}.\n\n"
        f"Note: This is a one-time verification link. "
        f"Future logins use your registered email — no passwords."
    )
    html = _html_wrap("Verify your TCA Blackjack account", f"""
        <h2 style="color:#e6edf3;margin:0 0 16px;font-size:20px;">Verify your email</h2>
        <p style="color:#8b949e;margin:0 0 24px;line-height:1.6;">
          Thanks for registering. Click the button below to verify your email address
          and activate your account.
        </p>
        <div style="text-align:center;margin:28px 0;">
          <a href="{url}"
             style="display:inline-block;padding:14px 32px;
                    background:#1f6feb;color:#ffffff;
                    text-decoration:none;border-radius:8px;
                    font-weight:700;font-size:15px;letter-spacing:0.5px;">
            Verify Email Address
          </a>
        </div>
        <p style="color:#4a5568;font-size:12px;margin:16px 0 0;text-align:center;">
          Link expires in {expires} · One-time use only
        </p>
        <p style="color:#4a5568;font-size:12px;margin:8px 0 0;text-align:center;">
          Or copy this URL: <span style="color:#58a6ff;">{url}</span>
        </p>
    """)
    return "Verify your TCA Blackjack account", text, html


def _render_magic_link(p: dict) -> tuple[str, str, str | None]:
    url = p['magic_url']
    text = (
        f"Click the link below to complete your account setup.\n\n"
        f"{url}\n\n"
        f"This link expires in {p['expires_in']} and can only be used once."
    )
    html = _html_wrap("Your TCA Blackjack setup link", f"""
        <h2 style="color:#e6edf3;margin:0 0 16px;font-size:20px;">Your setup link</h2>
        <p style="color:#8b949e;margin:0 0 24px;line-height:1.6;">
          Click the button below to complete your account setup.
        </p>
        <div style="text-align:center;margin:28px 0;">
          <a href="{url}"
             style="display:inline-block;padding:14px 32px;
                    background:#1f6feb;color:#ffffff;
                    text-decoration:none;border-radius:8px;
                    font-weight:700;font-size:15px;">
            Complete Setup
          </a>
        </div>
        <p style="color:#4a5568;font-size:12px;margin:16px 0 0;text-align:center;">
          Expires in {p['expires_in']} · One-time use only
        </p>
    """)
    return "Your TCA Blackjack setup link", text, html


def _render_password_reset(p: dict) -> tuple[str, str, str | None]:
    return (
        "Password reset",
        "[STUB — this message type is a honeypot and should never render]\n\n"
        f"{p.get('reset_url', '')}",
        None,
    )


def _render_game_invite(p: dict) -> tuple[str, str, str | None]:
    return (
        f"{p['inviter_name']} invited you to a game",
        f"{p['inviter_name']} has invited you to join their table: {p['table_name']}\n\n"
        f"Join here: {p['join_url']}",
        None,
    )


def _render_game_result_notify(p: dict) -> tuple[str, str, str | None]:
    result = p['result'].upper()
    change = p['net_change']
    sign = "+" if float(change) >= 0 else ""
    return (
        f"Game result: {result}",
        f"Your recent game has concluded.\n\nResult: {result}\nNet change: {sign}{change}\n\n"
        f"Log in to view your full session history.",
        None,
    )


def _render_session_summary(p: dict) -> tuple[str, str, str | None]:
    net = float(p['net_result'])
    sign = "+" if net >= 0 else ""
    return (
        "Your session summary",
        f"Session Summary\n---------------\n"
        f"Hands played : {p['hands_played']}\n"
        f"Net result   : {sign}{net}\n"
        f"Started      : {p['session_start']}\n"
        f"Ended        : {p['session_end']}\n\n"
        f"This summary is for your records only.",
        None,
    )


def _render_account_flag_notice(p: dict) -> tuple[str, str, str | None]:
    return (
        "Important notice regarding your account",
        f"An action has been taken on your TCA Blackjack account.\n\n"
        f"Reason      : {p['reason']}\n"
        f"Action taken: {p['action_taken']}\n\n"
        f"If you believe this is in error, please contact support.",
        None,
    )


def _render_transaction_receipt(p: dict) -> tuple[str, str, str | None]:
    return (
        f"Transaction receipt — {p['type'].upper()}",
        f"Transaction Receipt\n-------------------\n"
        f"Transaction ID : {p['transaction_id']}\n"
        f"Type           : {p['type']}\n"
        f"Amount         : {p['amount']}\n"
        f"Timestamp      : {p['timestamp']}\n"
        f"Balance after  : {p['balance_after']}\n\n"
        f"If you did not authorize this transaction, contact support immediately.",
        None,
    )
