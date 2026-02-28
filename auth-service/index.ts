/**
 * Auth Service v0.3.0
 * Language: TypeScript / Node.js
 *
 * Auth posture:
 *   - Register:        name + email → verify token → verification email
 *   - Verify:          /verify-token → exchange code → redirect to UI
 *   - Exchange:        /exchange {code} → JWT issued, session stored
 *   - Login (email):   email → JWT (dev fallback — remove before feature-complete)
 *   - Passkey register: POST /passkey/register/begin → /passkey/register/complete
 *   - Passkey login:    POST /passkey/login/begin   → /passkey/login/complete
 *
 * Storage:
 *   - PostgreSQL (auth-db): players, passkey_credentials, webauthn_challenges
 *   - Redis: sessions, verify tokens, exchange codes
 */

import http  from 'http';
import https from 'https';
import fs    from 'fs';
import crypto from 'crypto';
import jwt from 'jsonwebtoken';
import Redis from 'ioredis';
import { Pool } from 'pg';
import {
  generateRegistrationOptions,
  verifyRegistrationResponse,
  generateAuthenticationOptions,
  verifyAuthenticationResponse,
} from '@simplewebauthn/server';
import type {
  RegistrationResponseJSON,
  AuthenticationResponseJSON,
} from '@simplewebauthn/types';

const PORT    = parseInt(process.env.PORT ?? '3006');
const SERVICE = 'auth-service';

const JWT_SECRET     = process.env.JWT_SECRET ?? 'swarm-blackjack-dev-secret-change-in-production';
const JWT_EXPIRES_IN = 3600; // 60 minutes — extended for demo sessions

const REDIS_URL   = process.env.REDIS_URL   ?? 'redis://redis:6379';
const EMAIL_URL   = process.env.EMAIL_URL   ?? 'http://email-service:3008';
const BANK_URL    = process.env.BANK_URL    ?? 'https://bank-service:3005';

const TLS_CERT = process.env.TLS_CERT ?? '';
const TLS_KEY  = process.env.TLS_KEY  ?? '';
const TLS_CA   = process.env.TLS_CA   ?? '';

// mTLS agent for outbound calls to mTLS-enabled services (bank-service).
const mtlsAgent = (TLS_CERT && TLS_KEY && TLS_CA)
  ? new https.Agent({
      cert: fs.readFileSync(TLS_CERT),
      key:  fs.readFileSync(TLS_KEY),
      ca:   fs.readFileSync(TLS_CA),
    })
  : new https.Agent({ rejectUnauthorized: false });

// Post JSON to an mTLS endpoint. Returns parsed response body or null on error.
function mtlsPost(url: string, body: object): Promise<any> {
  return new Promise((resolve) => {
    const data    = JSON.stringify(body);
    const parsed  = new URL(url);
    const options = {
      hostname: parsed.hostname,
      port:     parsed.port || 443,
      path:     parsed.pathname,
      method:   'POST',
      headers:  { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) },
      agent:    mtlsAgent,
    };
    const req = https.request(options, (res) => {
      let buf = '';
      res.on('data', (chunk) => buf += chunk);
      res.on('end', () => { try { resolve(JSON.parse(buf)); } catch { resolve(null); } });
    });
    req.on('error', (e) => { console.error(`[mtlsPost] ${url} error: ${e.message}`); resolve(null); });
    req.write(data);
    req.end();
  });
}
const GATEWAY_URL = process.env.GATEWAY_URL ?? 'http://localhost:8021';
const UI_URL      = process.env.UI_URL      ?? 'http://localhost:8021';

// WebAuthn config — from auth.env, injected as K8s secret in production
const RP_NAME = process.env.WEBAUTHN_RP_NAME ?? 'Swarm Blackjack';
const RP_ID   = process.env.WEBAUTHN_RP_ID   ?? 'localhost';
const ORIGIN  = process.env.WEBAUTHN_ORIGIN  ?? 'http://localhost:8021';

// ── PostgreSQL ────────────────────────────────────────────────────────────────

const db = new Pool({
  host:     process.env.AUTH_DB_HOST     ?? 'auth-db',
  port:     parseInt(process.env.AUTH_DB_PORT ?? '5432'),
  database: process.env.AUTH_DB_NAME     ?? 'authdb',
  user:     process.env.AUTH_DB_USER     ?? 'authuser',
  password: process.env.AUTH_DB_PASSWORD ?? 'authpass_dev',
});

async function waitForDb(): Promise<void> {
  for (let i = 0; i < 15; i++) {
    try {
      await db.query('SELECT 1');
      console.log(`[${SERVICE}] PostgreSQL connected`);
      return;
    } catch {
      console.log(`[${SERVICE}] PostgreSQL not ready (${i + 1}/15), retrying...`);
      await new Promise(r => setTimeout(r, 2000));
    }
  }
  throw new Error('PostgreSQL unavailable after 15 attempts');
}

// ── Redis ─────────────────────────────────────────────────────────────────────

const redis = new Redis(REDIS_URL);
redis.on('connect', () => console.log(`[${SERVICE}] Redis connected`));
redis.on('error',   (e) => console.error(`[${SERVICE}] Redis error:`, e.message));

// ── Player operations ─────────────────────────────────────────────────────────

interface Player {
  id:               string;
  email:            string;
  name:             string;
  verified:         boolean;
  passkeyEnrolled:  boolean;
  createdAt:        string;
}

function rowToPlayer(row: any): Player {
  return {
    id:              row.id,
    email:           row.email,
    name:            row.name,
    verified:        row.verified,
    passkeyEnrolled: row.passkey_enrolled ?? false,
    createdAt:       row.created_at,
  };
}

async function findPlayerByEmail(email: string): Promise<Player | null> {
  const { rows } = await db.query('SELECT * FROM players WHERE email = $1', [email]);
  return rows[0] ? rowToPlayer(rows[0]) : null;
}

async function findPlayerById(id: string): Promise<Player | null> {
  const { rows } = await db.query('SELECT * FROM players WHERE id = $1', [id]);
  return rows[0] ? rowToPlayer(rows[0]) : null;
}

async function createPlayer(id: string, email: string, name: string): Promise<Player> {
  const { rows } = await db.query(
    'INSERT INTO players (id, email, name, verified) VALUES ($1, $2, $3, false) RETURNING *',
    [id, email, name]
  );
  return rowToPlayer(rows[0]);
}

async function markPlayerVerified(id: string): Promise<void> {
  await db.query('UPDATE players SET verified = true WHERE id = $1', [id]);
}

async function markPasskeyEnrolled(id: string): Promise<void> {
  await db.query('UPDATE players SET passkey_enrolled = true WHERE id = $1', [id]);
}

// ── Passkey credential operations ─────────────────────────────────────────────

interface StoredCredential {
  credentialId: string;
  playerId:     string;
  publicKey:    Buffer;
  counter:      number;
  deviceType:   string;
  backedUp:     boolean;
  transports:   string[];
}

function rowToCredential(row: any): StoredCredential {
  return {
    credentialId: row.credential_id,
    playerId:     row.player_id,
    publicKey:    row.public_key,
    counter:      parseInt(row.counter),
    deviceType:   row.device_type,
    backedUp:     row.backed_up,
    transports:   row.transports ?? [],
  };
}

async function getCredentialsByPlayerId(playerId: string): Promise<StoredCredential[]> {
  const { rows } = await db.query(
    'SELECT * FROM passkey_credentials WHERE player_id = $1',
    [playerId]
  );
  return rows.map(rowToCredential);
}

async function getCredentialById(credentialId: string): Promise<StoredCredential | null> {
  const { rows } = await db.query(
    'SELECT * FROM passkey_credentials WHERE credential_id = $1',
    [credentialId]
  );
  return rows[0] ? rowToCredential(rows[0]) : null;
}

async function saveCredential(cred: StoredCredential): Promise<void> {
  await db.query(
    `INSERT INTO passkey_credentials
       (credential_id, player_id, public_key, counter, device_type, backed_up, transports)
     VALUES ($1, $2, $3, $4, $5, $6, $7)
     ON CONFLICT (credential_id) DO UPDATE SET counter = $4`,
    [cred.credentialId, cred.playerId, cred.publicKey, cred.counter,
     cred.deviceType, cred.backedUp, cred.transports]
  );
}

async function updateCredentialCounter(credentialId: string, counter: number): Promise<void> {
  await db.query(
    'UPDATE passkey_credentials SET counter = $1 WHERE credential_id = $2',
    [counter, credentialId]
  );
}

// ── WebAuthn challenge operations ─────────────────────────────────────────────

async function storeChallenge(
  challenge: string,
  playerId: string | null,
  type: 'registration' | 'authentication'
): Promise<void> {
  const expiresAt = new Date(Date.now() + 5 * 60 * 1000); // 5 minutes
  await db.query(
    'INSERT INTO webauthn_challenges (challenge, player_id, type, expires_at) VALUES ($1, $2, $3, $4)',
    [challenge, playerId, type, expiresAt]
  );
}

async function consumeChallenge(
  challenge: string,
  type: 'registration' | 'authentication'
): Promise<string | null> {
  const { rows } = await db.query(
    `DELETE FROM webauthn_challenges
     WHERE challenge = $1 AND type = $2 AND expires_at > NOW()
     RETURNING player_id`,
    [challenge, type]
  );
  return rows[0]?.player_id ?? null;
}

// ── JWT + Session ─────────────────────────────────────────────────────────────

// Bootstrap JWT — scope: "enroll", 5 minute TTL
// Proves email ownership. Only valid on passkey enrollment endpoints.
// Never stored in localStorage — held in memory for ceremony duration only.
function issueBootstrapJWT(player: Player): string {
  return jwt.sign(
    { sub: player.id, email: player.email, name: player.name, scope: 'enroll', iss: 'swarm-blackjack' },
    JWT_SECRET,
    { expiresIn: 300 } // 5 minutes
  );
}

// Session JWT — scope: "session", 15 minute TTL
// Issued only after passkey ceremony succeeds. Stored in localStorage.
function issueSessionJWT(player: Player, sessionId: string): string {
  return jwt.sign(
    { sub: player.id, email: player.email, name: player.name, scope: 'session', sessionId, iss: 'swarm-blackjack' },
    JWT_SECRET,
    { expiresIn: JWT_EXPIRES_IN }
  );
}

function verifyJWT(token: string): jwt.JwtPayload | null {
  try { return jwt.verify(token, JWT_SECRET) as jwt.JwtPayload; }
  catch { return null; }
}

async function createSession(playerId: string): Promise<string> {
  const sessionId = crypto.randomUUID();
  await redis.setex(
    `session:${playerId}:${sessionId}`,
    JWT_EXPIRES_IN * 4,
    JSON.stringify({ playerId, createdAt: new Date().toISOString() })
  );
  return sessionId;
}

async function validateSession(playerId: string, sessionId: string): Promise<boolean> {
  return (await redis.get(`session:${playerId}:${sessionId}`)) !== null;
}

// ── Email calls ───────────────────────────────────────────────────────────────

async function sendVerificationEmail(player: Player): Promise<void> {
  const verifyToken = crypto.randomUUID();
  await redis.setex(`verify:${verifyToken}`, 86400, player.id);
  const verificationUrl = `${GATEWAY_URL}/verify?token=${verifyToken}`;

  const body = JSON.stringify({
    caller:       { service: SERVICE, request_id: crypto.randomUUID() },
    tier:         'system',
    message_type: 'verify_email',
    recipient:    { type: 'email', value: player.email },
    payload:      { verification_url: verificationUrl, expires_in: '24h' },
    options:      {},
  });

  try {
    const res  = await fetch(`${EMAIL_URL}/send`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body });
    const data = await res.json() as { status: string; message_id: string };
    console.log(`[${SERVICE}] Verification email queued: msgId=${data.message_id} to=${player.email}`);
  } catch (e: any) {
    console.error(`[${SERVICE}] Failed to send verification email:`, e.message);
  }
}

async function sendTransactionReceipt(player: Player, txData: {
  transactionId: string; amount: string; type: string; timestamp: string; balanceAfter: string;
}): Promise<void> {
  const body = JSON.stringify({
    caller:       { service: SERVICE, request_id: crypto.randomUUID() },
    tier:         'restricted',
    message_type: 'transaction_receipt',
    recipient:    { type: 'user_id', value: player.id },
    payload: {
      transaction_id: txData.transactionId, amount: txData.amount,
      type: txData.type, timestamp: txData.timestamp, balance_after: txData.balanceAfter,
    },
    options: {},
  });
  try {
    await fetch(`${EMAIL_URL}/send`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body });
  } catch (e: any) {
    console.error(`[${SERVICE}] Failed to send transaction receipt:`, e.message);
  }
}

// ── Bank calls ────────────────────────────────────────────────────────────────

async function ensureBankAccount(player: Player): Promise<void> {
  try {
    await mtlsPost(`${BANK_URL}/account`, { playerId: player.id, startingBalance: '1000.00' });
    console.log(`[${SERVICE}] Bank account ensured for player=${player.id}`);
  } catch (e: any) {
    console.error(`[${SERVICE}] Failed to create bank account:`, e.message);
  }
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

function jsonResponse(res: http.ServerResponse, status: number, body: object): void {
  res.writeHead(status, {
    'Content-Type': 'application/json',
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
    'Access-Control-Allow-Headers': 'Content-Type, Authorization',
  });
  res.end(JSON.stringify(body));
}

function htmlResponse(res: http.ServerResponse, status: number, html: string): void {
  res.writeHead(status, { 'Content-Type': 'text/html; charset=utf-8' });
  res.end(html);
}

function readBody(req: http.IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end',  () => resolve(body));
    req.on('error', reject);
  });
}

function getBearerToken(req: http.IncomingMessage): string | null {
  const auth = req.headers.authorization ?? '';
  return auth.startsWith('Bearer ') ? auth.slice(7) : null;
}

function decodeClientDataChallenge(clientDataJSON: string): string {
  return JSON.parse(Buffer.from(clientDataJSON, 'base64url').toString()).challenge;
}

// ── Server ────────────────────────────────────────────────────────────────────

const requestHandler = async (req: http.IncomingMessage, res: http.ServerResponse) => {
  const url    = new URL(req.url ?? '/', `http://localhost:${PORT}`);
  const method = req.method ?? 'GET';

  if (method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type, Authorization',
    });
    res.end();
    return;
  }

  console.log(`[${SERVICE}] ${method} ${url.pathname}`);

  // ── Health ──────────────────────────────────────────────────────────────────

  if (method === 'GET' && url.pathname === '/health') {
    let dbStatus = 'disconnected';
    try { await db.query('SELECT 1'); dbStatus = 'connected'; } catch {}
    jsonResponse(res, 200, {
      status: 'healthy', service: SERVICE, language: 'TypeScript',
      redis: redis.status === 'ready' ? 'connected' : 'disconnected',
      db: dbStatus,
      webauthn: { rpId: RP_ID, rpName: RP_NAME, origin: ORIGIN },
    });
    return;
  }

  // ── GET /users/{id}/email ───────────────────────────────────────────────────

  if (method === 'GET' && url.pathname.startsWith('/users/') && url.pathname.endsWith('/email')) {
    const playerId = url.pathname.split('/')[2];
    const player   = await findPlayerById(playerId);
    if (!player) { jsonResponse(res, 404, { error: 'player not found' }); return; }
    jsonResponse(res, 200, { playerId: player.id, email: player.email });
    return;
  }

  // ── POST /register ──────────────────────────────────────────────────────────

  if (method === 'POST' && url.pathname === '/register') {
    const body = await readBody(req);
    let data: any;
    try { data = JSON.parse(body || '{}'); } catch { jsonResponse(res, 400, { error: 'invalid JSON' }); return; }

    const email = (data.email ?? '').trim().toLowerCase();
    const name  = (data.name  ?? '').trim();

    if (!email || !name)                              { jsonResponse(res, 400, { error: 'email and name required' }); return; }
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email))   { jsonResponse(res, 400, { error: 'invalid email address' }); return; }
    if (await findPlayerByEmail(email))               { jsonResponse(res, 409, { error: 'email already registered' }); return; }

    const player = await createPlayer(crypto.randomUUID(), email, name);
    await ensureBankAccount(player);
    sendVerificationEmail(player); // fire and forget

    console.log(`[${SERVICE}] Registered (unverified): id=${player.id} email=${email}`);
    jsonResponse(res, 201, {
      registered: true,
      message:    'Verification email sent. Check your inbox and click the link to activate your account.',
      email:      player.email,
    });
    return;
  }

  // ── GET /verify-token ───────────────────────────────────────────────────────

  if (method === 'GET' && url.pathname === '/verify-token') {
    const token = url.searchParams.get('token');
    if (!token) { htmlResponse(res, 400, errorPage('Missing verification token.', UI_URL)); return; }

    const playerId = await redis.getdel(`verify:${token}`);
    if (!playerId) {
      htmlResponse(res, 400, errorPage('Verification link is invalid or has already been used.', UI_URL));
      return;
    }

    const player = await findPlayerById(playerId);
    if (!player) { htmlResponse(res, 400, errorPage('Account not found.', UI_URL)); return; }

    await markPlayerVerified(playerId);

    const code = crypto.randomUUID();
    await redis.setex(`exchange:${code}`, 60, playerId);

    console.log(`[${SERVICE}] Email verified: player=${playerId}`);
    res.writeHead(302, { Location: `${UI_URL}?exchange=${code}` });
    res.end();
    return;
  }

  // ── POST /exchange ──────────────────────────────────────────────────────────

  if (method === 'POST' && url.pathname === '/exchange') {
    const body = await readBody(req);
    let data: any;
    try { data = JSON.parse(body || '{}'); } catch { jsonResponse(res, 400, { error: 'invalid JSON' }); return; }

    const playerId = await redis.getdel(`exchange:${data.code ?? ''}`);
    if (!playerId) { jsonResponse(res, 401, { error: 'invalid or expired exchange code' }); return; }

    const player = await findPlayerById(playerId);
    if (!player) { jsonResponse(res, 404, { error: 'player not found' }); return; }

    // Issue bootstrap JWT — scope: "enroll", 5 minute TTL
    // Session JWT is NOT issued here. Client must complete passkey enrollment first.
    const bootstrapToken = issueBootstrapJWT(player);

    console.log(`[${SERVICE}] Exchange: issued bootstrap JWT for player=${playerId}`);
    jsonResponse(res, 200, {
      bootstrapToken,
      requiresEnrollment: true,
      playerId:    player.id,
      playerName:  player.name,
      email:       player.email,
    });
    return;
  }

  // ── POST /passkey/register/begin ────────────────────────────────────────────
  // Requires valid JWT — player must already have a session to enroll a passkey

  if (method === 'POST' && url.pathname === '/passkey/register/begin') {
    const token   = getBearerToken(req);
    const payload = token ? verifyJWT(token) : null;
    if (!payload) { jsonResponse(res, 401, { error: 'authentication required' }); return; }

    // Accept enroll scope (first enrollment) or session scope (adding another passkey)
    const scope = (payload as any).scope;
    if (scope !== 'enroll' && scope !== 'session') {
      jsonResponse(res, 403, { error: 'invalid token scope for passkey registration' });
      return;
    }

    const player = await findPlayerById(payload.sub!);
    if (!player)  { jsonResponse(res, 404, { error: 'player not found' }); return; }

    const existingCreds = await getCredentialsByPlayerId(player.id);

    const options = await generateRegistrationOptions({
      rpName:  RP_NAME,
      rpID:    RP_ID,
      userName: player.email,
      userDisplayName: player.name,
      attestationType: 'none',
      excludeCredentials: existingCreds.map(c => ({
        id:         c.credentialId,
        transports: c.transports as any,
      })),
      authenticatorSelection: {
        residentKey:      'preferred',
        userVerification: 'preferred',
      },
    });

    await storeChallenge(options.challenge, player.id, 'registration');
    console.log(`[${SERVICE}] Passkey register begin: player=${player.id}`);
    jsonResponse(res, 200, options);
    return;
  }

  // ── POST /passkey/register/complete ─────────────────────────────────────────

  if (method === 'POST' && url.pathname === '/passkey/register/complete') {
    const token   = getBearerToken(req);
    const payload = token ? verifyJWT(token) : null;
    if (!payload) { jsonResponse(res, 401, { error: 'authentication required' }); return; }

    const scope = (payload as any).scope;
    if (scope !== 'enroll' && scope !== 'session') {
      jsonResponse(res, 403, { error: 'invalid token scope for passkey registration' });
      return;
    }

    const player = await findPlayerById(payload.sub!);
    if (!player)  { jsonResponse(res, 404, { error: 'player not found' }); return; }

    const body = await readBody(req);
    let credential: RegistrationResponseJSON;
    try { credential = JSON.parse(body); } catch { jsonResponse(res, 400, { error: 'invalid JSON' }); return; }

    const challenge    = decodeClientDataChallenge(credential.response.clientDataJSON);
    const storedPlayer = await consumeChallenge(challenge, 'registration');

    if (!storedPlayer || storedPlayer !== player.id) {
      jsonResponse(res, 400, { error: 'invalid or expired challenge' });
      return;
    }

    try {
      const verification = await verifyRegistrationResponse({
        response:          credential,
        expectedChallenge: challenge,
        expectedOrigin:    ORIGIN,
        expectedRPID:      RP_ID,
      });

      if (!verification.verified || !verification.registrationInfo) {
        jsonResponse(res, 400, { error: 'passkey verification failed' });
        return;
      }

      const { credentialID, credentialPublicKey, credentialDeviceType, credentialBackedUp } = verification.registrationInfo;

      await saveCredential({
        credentialId: credentialID,
        playerId:     player.id,
        publicKey:    Buffer.from(credentialPublicKey),
        counter:      verification.registrationInfo.counter,
        deviceType:   credentialDeviceType,
        backedUp:     credentialBackedUp,
        transports:   (credential.response as any).transports ?? [],
      });

      await markPasskeyEnrolled(player.id);
      console.log(`[${SERVICE}] Passkey enrolled: player=${player.id} credId=${credentialID} scope=${scope}`);

      // First-time enrollment (bootstrap scope) → issue session JWT now
      // Adding another passkey (session scope) → just return success
      if (scope === 'enroll') {
        const sessionId = await createSession(player.id);
        const accessToken = issueSessionJWT(player, sessionId);
        jsonResponse(res, 200, {
          verified: true,
          accessToken,
          expiresIn:  JWT_EXPIRES_IN,
          playerId:   player.id,
          playerName: player.name,
          email:      player.email,
        });
      } else {
        jsonResponse(res, 200, { verified: true });
      }
    } catch (e: any) {
      console.error(`[${SERVICE}] Passkey registration error:`, e.message);
      jsonResponse(res, 400, { error: e.message });
    }
    return;
  }

  // ── POST /passkey/login/begin ───────────────────────────────────────────────

  if (method === 'POST' && url.pathname === '/passkey/login/begin') {
    const body = await readBody(req);
    let data: any;
    try { data = JSON.parse(body || '{}'); } catch { jsonResponse(res, 400, { error: 'invalid JSON' }); return; }

    const email  = (data.email ?? '').trim().toLowerCase();
    const player = email ? await findPlayerByEmail(email) : null;

    // Return options even if player not found — don't reveal account existence
    const allowCredentials = player
      ? (await getCredentialsByPlayerId(player.id)).map(c => ({
          id:         c.credentialId,
          transports: c.transports as any,
        }))
      : [];

    const options = await generateAuthenticationOptions({
      rpID:             RP_ID,
      allowCredentials,
      userVerification: 'preferred',
    });

    await storeChallenge(options.challenge, player?.id ?? null, 'authentication');
    console.log(`[${SERVICE}] Passkey login begin: email=${email} credentials=${allowCredentials.length}`);
    jsonResponse(res, 200, options);
    return;
  }

  // ── POST /passkey/login/complete ────────────────────────────────────────────

  if (method === 'POST' && url.pathname === '/passkey/login/complete') {
    const body = await readBody(req);
    let assertion: AuthenticationResponseJSON;
    try { assertion = JSON.parse(body); } catch { jsonResponse(res, 400, { error: 'invalid JSON' }); return; }

    const challenge = decodeClientDataChallenge(assertion.response.clientDataJSON);
    const playerId  = await consumeChallenge(challenge, 'authentication');

    if (!playerId) { jsonResponse(res, 400, { error: 'invalid or expired challenge' }); return; }

    const storedCred = await getCredentialById(assertion.id);
    if (!storedCred || storedCred.playerId !== playerId) {
      jsonResponse(res, 401, { error: 'credential not found or belongs to different account' });
      return;
    }

    const player = await findPlayerById(playerId);
    if (!player) { jsonResponse(res, 404, { error: 'player not found' }); return; }

    try {
      const verification = await verifyAuthenticationResponse({
        response:          assertion,
        expectedChallenge: challenge,
        expectedOrigin:    ORIGIN,
        expectedRPID:      RP_ID,
        authenticator: {
          credentialID:        storedCred.credentialId,
          credentialPublicKey: new Uint8Array(storedCred.publicKey),
          counter:             storedCred.counter,
          transports:          storedCred.transports as any,
        },
      });

      if (!verification.verified) {
        jsonResponse(res, 401, { error: 'passkey verification failed' });
        return;
      }

      // Update counter — prevents replay attacks
      await updateCredentialCounter(storedCred.credentialId, verification.authenticationInfo.newCounter);

      const sessionId = await createSession(player.id);
      const token     = issueSessionJWT(player, sessionId);

      console.log(`[${SERVICE}] Passkey login success: player=${player.id}`);
      jsonResponse(res, 200, {
        accessToken: token, expiresIn: JWT_EXPIRES_IN,
        playerId: player.id, playerName: player.name, email: player.email,
      });
    } catch (e: any) {
      console.error(`[${SERVICE}] Passkey auth error:`, e.message);
      jsonResponse(res, 401, { error: e.message });
    }
    return;
  }

  // ── POST /validate ──────────────────────────────────────────────────────────

  if (method === 'POST' && url.pathname === '/validate') {
    const body = await readBody(req);
    let data: any;
    try { data = JSON.parse(body || '{}'); } catch { jsonResponse(res, 400, { error: 'invalid JSON' }); return; }

    const payload = verifyJWT(data.token ?? '');
    if (!payload) { jsonResponse(res, 401, { error: 'invalid or expired token' }); return; }

    const valid = await validateSession(payload.sub!, payload.sessionId);
    if (!valid)   { jsonResponse(res, 401, { error: 'session expired or revoked' }); return; }

    jsonResponse(res, 200, {
      valid: true, playerId: payload.sub, email: payload.email, name: payload.name,
      scope: (payload as any).scope ?? 'session',
      expiresAt: new Date((payload.exp ?? 0) * 1000).toISOString(),
    });
    return;
  }

  // ── POST /notify/transaction ────────────────────────────────────────────────

  if (method === 'POST' && url.pathname === '/notify/transaction') {
    const body = await readBody(req);
    let data: any;
    try { data = JSON.parse(body || '{}'); } catch { jsonResponse(res, 400, { error: 'invalid JSON' }); return; }

    const player = await findPlayerById(data.playerId ?? '');
    if (!player) { jsonResponse(res, 404, { error: 'player not found' }); return; }

    sendTransactionReceipt(player, {
      transactionId: data.transactionId, amount: data.amount,
      type: data.type, timestamp: data.timestamp, balanceAfter: data.balanceAfter,
    });
    jsonResponse(res, 202, { queued: true });
    return;
  }

  // ── POST /policy/check (OPA stub) ───────────────────────────────────────────

  if (method === 'POST' && url.pathname === '/policy/check') {
    const body = await readBody(req);
    const data = JSON.parse(body || '{}');
    jsonResponse(res, 200, { allowed: true, playerId: data.playerId, action: data.action, stub: true });
    return;
  }

  // ── GET /dev/demo-token ──────────────────────────────────────────────────────
  // Issues a long-lived session JWT for the demo player.
  // No auth required — demo player is public by design.
  // Gate this off before production.

  if (method === 'GET' && url.pathname === '/dev/demo-token') {
    const DEMO_PLAYER_ID = 'player-00000000-0000-0000-0000-000000000001';
    const token = jwt.sign(
      {
        sub:       DEMO_PLAYER_ID,
        email:     'demo@swarm-blackjack.local',
        name:      'Player 1',
        scope:     'session',
        sessionId: 'demo-session',
        iss:       'swarm-blackjack',
      },
      JWT_SECRET,
      { expiresIn: '24h' }
    );
    jsonResponse(res, 200, { accessToken: token, playerId: DEMO_PLAYER_ID });
    return;
  }

  // ── POST /dev/reset ─────────────────────────────────────────────────────────
  // DEV ONLY — wipes all players, credentials, challenges, and Redis sessions.
  // Gate this off before production.

  if (method === 'POST' && url.pathname === '/dev/reset') {
    // Truncate in FK-safe order: child tables first, then players
    await db.query('TRUNCATE TABLE webauthn_challenges RESTART IDENTITY CASCADE');
    await db.query('TRUNCATE TABLE passkey_credentials RESTART IDENTITY CASCADE');
    await db.query('TRUNCATE TABLE players RESTART IDENTITY CASCADE');
    await redis.flushdb();
    console.log(`[${SERVICE}] DEV RESET: all players, credentials, sessions wiped`);
    jsonResponse(res, 200, { reset: true, service: SERVICE });
    return;
  }

  jsonResponse(res, 404, { error: 'not found' });
};

// Create server — mTLS if cert env vars present, plain HTTP otherwise
const server = (TLS_CERT && TLS_KEY && TLS_CA)
  ? https.createServer({
      cert: fs.readFileSync(TLS_CERT),
      key:  fs.readFileSync(TLS_KEY),
      ca:   fs.readFileSync(TLS_CA),
      requestCert: true,
      rejectUnauthorized: true,
    }, requestHandler)
  : http.createServer(requestHandler);

// ── Error page ────────────────────────────────────────────────────────────────

function errorPage(message: string, uiUrl: string): string {
  return `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Verification Error — Swarm Blackjack</title>
  <style>
    body { background:#0d1117; color:#e6edf3; font-family:system-ui,sans-serif;
           display:flex; align-items:center; justify-content:center; min-height:100vh; margin:0; }
    .box { background:#161b22; border:1px solid #e53e3e; border-radius:12px;
           padding:32px 40px; max-width:420px; text-align:center; }
    h2 { color:#fc8181; margin:0 0 16px; }
    p  { color:#8b949e; line-height:1.6; margin:0 0 24px; }
    a  { display:inline-block; padding:10px 24px; background:#1f6feb;
         color:#fff; text-decoration:none; border-radius:8px; font-weight:600; }
  </style>
</head>
<body>
  <div class="box">
    <h2>Verification Failed</h2>
    <p>${message}</p>
    <a href="${uiUrl}">Back to Game</a>
  </div>
</body>
</html>`;
}

// ── Startup ───────────────────────────────────────────────────────────────────

async function main() {
  await waitForDb();

  // Idempotent migration — safe on every restart
  await db.query(`ALTER TABLE players ADD COLUMN IF NOT EXISTS passkey_enrolled BOOLEAN NOT NULL DEFAULT false`);
  console.log(`[${SERVICE}] Migrations complete`);

  server.listen(PORT, () => {
    const mode = (TLS_CERT && TLS_KEY && TLS_CA) ? '(mTLS)' : '(plaintext)';
    console.log(`[${SERVICE}] listening on :${PORT} ${mode}`);
    console.log(`[${SERVICE}] WebAuthn: rpId=${RP_ID} origin=${ORIGIN}`);
    console.log(`[${SERVICE}] Gateway: ${GATEWAY_URL} | UI: ${UI_URL}`);
  });
}

main().catch(err => {
  console.error(`[${SERVICE}] Fatal startup error:`, err);
  process.exit(1);
});
