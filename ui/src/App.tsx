import React, { useState, useEffect, useCallback } from 'react';
import { useGameState, DEMO_TABLE_ID, DEMO_PLAYER_ID } from './hooks/useGameState';
import { GameTable } from './components/GameTable';
import { ObservabilityDrawer } from './components/ObservabilityDrawer';
import { AuthModal } from './components/AuthModal';
import { EnrollmentModal } from './components/EnrollmentModal';
import { TransactionModal } from './components/TransactionModal';
import { SessionHistoryDrawer } from './components/SessionHistoryDrawer';

const SESSION_KEY = 'tca_session';
const GATEWAY_URL = import.meta.env.VITE_GATEWAY_URL || '';

interface Session {
  accessToken: string;
  playerId:    string;
  playerName:  string;
  email:       string;
  expiresAt:   number;
}

function loadSession(): Session | null {
  try {
    const raw = localStorage.getItem(SESSION_KEY);
    if (!raw) return null;
    const s: Session = JSON.parse(raw);
    if (Date.now() > s.expiresAt) { localStorage.removeItem(SESSION_KEY); return null; }
    return s;
  } catch { return null; }
}

function saveSession(data: {
  accessToken: string; expiresIn: number;
  playerId: string; playerName: string; email: string;
}): Session {
  const session: Session = {
    accessToken: data.accessToken,
    playerId:    data.playerId,
    playerName:  data.playerName,
    email:       data.email,
    expiresAt:   Date.now() + data.expiresIn * 1000,
  };
  localStorage.setItem(SESSION_KEY, JSON.stringify(session));
  return session;
}

interface EnrollmentPending {
  bootstrapToken: string;
  playerId:       string;
  playerName:     string;
  email:          string;
}

function App() {
  const [session, setSession]               = useState<Session | null>(loadSession);
  const [showModal, setShowModal]           = useState(false);
  const [enrollment, setEnrollment]         = useState<EnrollmentPending | null>(null);
  const [showTxModal, setShowTxModal]       = useState(false);
  const [balance, setBalance]               = useState<number | null>(null);
  const [demoToken, setDemoToken]           = useState<string>('');
  const [playerTableId, setPlayerTableId]   = useState<string | null>(null);
  const [showRestock, setShowRestock]       = useState(false);
  const [restocking, setRestocking]         = useState(false);
  const [demoPaused, setDemoPaused]           = useState(false);

  // Which table are we watching?
  const activeTableId  = playerTableId ?? DEMO_TABLE_ID;
  const isDemo         = playerTableId === null;
  const activePlayerId = isDemo ? DEMO_PLAYER_ID : (session?.playerId ?? DEMO_PLAYER_ID);
  const activeToken    = isDemo ? demoToken : session?.accessToken;

  const { gameState, connected, error, rounds, sendAction } = useGameState({
    tableId:  activeTableId,
    playerId: activePlayerId,
    token:    activeToken,
  });

  // Fetch demo JWT once on mount
  useEffect(() => {
    fetch(`${GATEWAY_URL}/dev/demo-token`)
      .then(r => r.ok ? r.json() : null)
      .then(d => { if (d?.accessToken) setDemoToken(d.accessToken); })
      .catch(() => {});
  }, []);

  // Balance fetch
  const fetchBalance = useCallback(() => {
    const playerId = isDemo ? DEMO_PLAYER_ID : session?.playerId;
    if (!playerId) return;
    // Only attach token once we have one — avoids 401 on initial mount
    const headers: Record<string, string> = {};
    if (activeToken && activeToken.length > 10) headers['Authorization'] = `Bearer ${activeToken}`;
    fetch(`${GATEWAY_URL}/api/bank/balance?playerId=${encodeURIComponent(playerId)}`, { headers })
      .then(r => r.ok ? r.json() : null)
      .then(data => { if (data?.balance !== undefined) setBalance(Number(data.balance)); })
      .catch(() => {});
  }, [session, isDemo, activeToken]);

  useEffect(() => { fetchBalance(); }, [session, playerTableId, fetchBalance]);

  // Balance pub-sub
  useEffect(() => {
    const pid = isDemo ? DEMO_PLAYER_ID : session?.playerId;
    if (!pid) return;
    const es = new EventSource(`${GATEWAY_URL}/api/bank/balance/stream`);
    es.addEventListener('balance_update', (e: MessageEvent) => {
      try {
        const evt = JSON.parse(e.data);
        if (evt.playerId === pid) setBalance(Number(evt.balance));
      } catch { /* ignore */ }
    });
    es.onerror = () => console.warn('[bank] balance stream disconnected');
    return () => es.close();
  }, [isDemo, session]);

  // Restock check: use authoritative bank balance, not game-state chips
  // (game-state chips can be stale if bank parsing failed)
  useEffect(() => {
    if (isDemo || !gameState || gameState.phase !== 'waiting') return;
    if (balance !== null && balance <= 0) setShowRestock(true);
  }, [gameState?.phase, isDemo, balance]);

  // Email verification redirect
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const code   = params.get('exchange');
    if (!code) return;
    window.history.replaceState({}, '', window.location.pathname);
    fetch('/api/auth/exchange', {
      method:  'POST',
      headers: { 'Content-Type': 'application/json' },
      body:    JSON.stringify({ code }),
    })
      .then(r => r.json())
      .then((data: any) => {
        if (data.requiresEnrollment && data.bootstrapToken) {
          setEnrollment({ bootstrapToken: data.bootstrapToken, playerId: data.playerId, playerName: data.playerName, email: data.email });
        } else if (data.accessToken) {
          setSession(saveSession(data));
        }
      })
      .catch(e => console.error('[auth] exchange failed:', e));
  }, []);

  // JWT expiry
  useEffect(() => {
    if (!session) return;
    const ms = session.expiresAt - Date.now();
    if (ms <= 0) { setSession(null); return; }
    const t = setTimeout(() => setSession(null), ms);
    return () => clearTimeout(t);
  }, [session]);

  // Start Your Game: create player table, switch stream
  const handleStartGame = async () => {
    if (!session) return;
    try {
      const res = await fetch(`${GATEWAY_URL}/api/game/create`, {
        method:  'POST',
        headers: {
          'Content-Type':  'application/json',
          'Authorization': `Bearer ${session.accessToken}`,
        },
        body: JSON.stringify({ playerId: session.playerId, playerName: session.playerName }),
      });
      if (!res.ok) throw new Error('Failed to create table');
      const data = await res.json();
      setPlayerTableId(data.tableId);
      setShowRestock(false);
    } catch (e) {
      console.error('[game] table create failed:', e);
    }
  };

  const handleBackToDemo = () => {
    setPlayerTableId(null);
    setShowRestock(false);
  };

  const handleRestock = async () => {
    if (!session) return;
    setRestocking(true);
    try {
      await fetch(`${GATEWAY_URL}/api/bank/deposit`, {
        method:  'POST',
        headers: {
          'Content-Type':  'application/json',
          'Authorization': `Bearer ${session.accessToken}`,
        },
        body: JSON.stringify({ playerId: session.playerId, amount: '1000.00' }),
      });
      setShowRestock(false);
      setBalance(1000);
    } catch (e) {
      console.error('[bank] restock failed:', e);
    } finally {
      setRestocking(false);
    }
  };

  const handleToggleDemo = async () => {
    try {
      const res = await fetch(`${GATEWAY_URL}/api/game/demo/pause`, { method: 'POST' });
      const data = await res.json();
      setDemoPaused(data.paused);
    } catch (e) { console.error('[demo] pause toggle failed:', e); }
  };

  const handleDevReset = async () => {
    if (!window.confirm('DEV RESET: wipe all players, sessions, and balances?')) return;
    try {
      const res  = await fetch('/dev/reset', { method: 'POST' });
      const data = await res.json();
      localStorage.removeItem(SESSION_KEY);
      setSession(null); setEnrollment(null); setPlayerTableId(null);
      const lines = Object.entries(data.results ?? {}).map(([k, v]) => `  ${k}: ${v}`).join('\n');
      alert(`Reset complete:\n${lines}`);
    } catch { alert('Reset failed — check console'); }
  };

  const handleAuthSuccess = (result: { accessToken: string; expiresIn: number; playerId: string; playerName: string; email: string; }) => {
    setSession(saveSession(result));
    setShowModal(false);
  };

  const handleSignOut = () => {
    localStorage.removeItem(SESSION_KEY);
    setSession(null);
    setPlayerTableId(null);
  };

  return (
    <div style={{
      minHeight: '100vh', background: '#0d1117', color: '#e6edf3',
      fontFamily: "'Segoe UI', system-ui, sans-serif",
      padding: '16px', display: 'flex', flexDirection: 'column',
      gap: 16, maxWidth: 960, margin: '0 auto',
    }}>

      {/* Observability drawer — left side, dev tool */}
      <ObservabilityDrawer />

      {/* ── Header ── */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div>
          <h1 style={{ margin: 0, fontSize: '1.4rem', color: '#58a6ff', letterSpacing: 2, textTransform: 'uppercase' }}>
            TCA Blackjack
          </h1>
          <div style={{ fontSize: '0.65rem', color: '#8b949e', letterSpacing: 1 }}>
            Polyglot Microservices · Zero Trust · PoC
          </div>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          {/* Connection status */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <div style={{
              width: 8, height: 8, borderRadius: '50%',
              background: connected ? '#38a169' : '#e53e3e',
              boxShadow: connected ? '0 0 8px #38a169' : undefined,
            }} />
            <span style={{ fontSize: '0.7rem', color: '#8b949e' }}>
              {connected ? 'Connected' : 'Connecting...'}
            </span>
          </div>

          {/* DEV ONLY */}
          <button onClick={handleToggleDemo} title="Pause/resume demo loop" style={{
            padding: '5px 10px', background: 'none',
            border: `1px solid ${demoPaused ? '#2d5a27' : '#1a3a2a'}`, borderRadius: 6,
            color: demoPaused ? '#68d391' : '#4a6a5a', fontSize: '0.68rem', cursor: 'pointer',
          }}>{demoPaused ? '▶ Demo' : '⏸ Demo'}</button>
          <button onClick={handleDevReset} title="DEV: wipe all accounts" style={{
            padding: '5px 10px', background: 'none',
            border: '1px solid #4a1515', borderRadius: 6,
            color: '#6b2929', fontSize: '0.68rem', cursor: 'pointer',
          }}>⚠ Reset DB</button>

          {session ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              {/* Chip balance */}
              <div style={{
                display: 'flex', alignItems: 'center', gap: 6,
                background: '#1a2535',
                border: `1px solid ${isDemo ? '#f59e0b44' : '#1f6feb44'}`,
                borderRadius: 8, padding: '5px 12px',
              }}>
                <span style={{ fontSize: '0.9rem' }}>🪙</span>
                <span style={{
                  fontSize: '0.85rem', fontWeight: 700,
                  color: balance !== null ? '#ecc94b' : '#4a5568',
                  fontFamily: 'monospace', letterSpacing: 0.5,
                }}>
                  {balance !== null ? balance.toLocaleString() : '···'}
                </span>
                {isDemo && (
                  <span style={{ fontSize: '0.55rem', fontWeight: 700, color: '#f59e0b', letterSpacing: 1, marginLeft: 2 }}>
                    DEMO
                  </span>
                )}
              </div>

              {/* Transactions */}
              <button onClick={() => setShowTxModal(true)} style={{
                padding: '5px 10px', background: 'none',
                border: '1px solid #30363d', borderRadius: 6,
                color: '#8b949e', fontSize: '0.72rem', cursor: 'pointer',
              }}>Transactions</button>

              {/* Game mode toggle */}
              {isDemo ? (
                <button
                  onClick={handleStartGame}
                  style={{
                    padding: '6px 14px',
                    background: 'linear-gradient(135deg, #1f6feb22, #38a16922)',
                    border: '1px solid #38a169', borderRadius: 6,
                    color: '#68d391', fontSize: '0.75rem', fontWeight: 700,
                    cursor: 'pointer', letterSpacing: 0.5, whiteSpace: 'nowrap',
                  }}
                >▶ Start Your Game</button>
              ) : (
                <button
                  onClick={handleBackToDemo}
                  style={{
                    padding: '6px 14px', background: 'none',
                    border: '1px solid #38a169', borderRadius: 6,
                    color: '#38a169', fontSize: '0.72rem', cursor: 'pointer',
                  }}
                >● YOUR GAME</button>
              )}

              {/* Player info */}
              <div style={{ textAlign: 'right' }}>
                <div style={{ fontSize: '0.78rem', color: '#e2e8f0', fontWeight: 600 }}>{session.playerName}</div>
                <div style={{ fontSize: '0.65rem', color: '#8b949e' }}>{session.email}</div>
              </div>
              <button onClick={handleSignOut} style={{
                padding: '5px 12px', background: 'none',
                border: '1px solid #30363d', borderRadius: 6,
                color: '#8b949e', fontSize: '0.72rem', cursor: 'pointer',
              }}>Sign out</button>
            </div>
          ) : (
            <button onClick={() => setShowModal(true)} style={{
              padding: '7px 16px', background: '#1f6feb22',
              border: '1px solid #1f6feb', borderRadius: 8,
              color: '#58a6ff', fontWeight: 600, fontSize: '0.8rem',
              cursor: 'pointer', letterSpacing: 0.5,
            }}>Login / Register</button>
          )}
        </div>
      </div>

      {error && (
        <div style={{
          background: '#fc818144', color: '#fc8181', border: '1px solid #fc818166',
          padding: '8px 16px', borderRadius: 6, fontSize: '0.8rem', fontWeight: 600,
        }}>{error}</div>
      )}

      {/* Game table */}
      {gameState ? (
        <GameTable
          gameState={gameState}
          onAction={sendAction}
          myPlayerId={isDemo ? undefined : session?.playerId}
          isDemo={isDemo}
          authoritativeBalance={isDemo ? null : balance}
        />
      ) : (
        <div style={{
          background: 'radial-gradient(ellipse at center, #1a4731 0%, #0d2818 100%)',
          borderRadius: 24, padding: 48, textAlign: 'center',
          border: '3px solid rgba(255,255,255,0.1)',
        }}>
          <div style={{ color: '#68d391', fontSize: '1.5rem', marginBottom: 8 }}>⟳</div>
          <div style={{ color: '#a0aec0' }}>Connecting to game state service...</div>
        </div>
      )}

      {/* Table metadata */}
      {gameState && (
        <div style={{ display: 'flex', gap: 16, fontSize: '0.65rem', color: '#4a5568', fontFamily: 'monospace', flexWrap: 'wrap' }}>
          <span>Table: {gameState.tableId.slice(0, 16)}...</span>
          <span>Phase: {gameState.phase}</span>
          <span>By: <strong style={{ color: '#38a169' }}>{gameState.handledBy}</strong></span>
          <span>{new Date(gameState.timestamp).toLocaleTimeString()}</span>
          {!isDemo && session && <span style={{ color: '#58a6ff' }}>● {session.playerName}</span>}
        </div>
      )}



      {/* ── Modals ── */}
      {showModal && (
        <AuthModal onSuccess={handleAuthSuccess} onClose={() => setShowModal(false)} />
      )}

      {enrollment && (
        <EnrollmentModal
          bootstrapToken={enrollment.bootstrapToken}
          playerId={enrollment.playerId}
          playerName={enrollment.playerName}
          email={enrollment.email}
          onSuccess={(result) => { setEnrollment(null); setSession(saveSession(result)); }}
        />
      )}

      {showTxModal && (
        <TransactionModal
          playerId={isDemo ? DEMO_PLAYER_ID : (session?.playerId ?? '')}
          accessToken={isDemo ? demoToken : (session?.accessToken ?? '')}
          isDemo={isDemo}
          onClose={() => setShowTxModal(false)}
        />
      )}

      {/* Restock popup */}
      {showRestock && session && (
        <div style={{
          position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.75)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          zIndex: 500,
        }}>
          <div style={{
            background: '#161b22', border: '1px solid #30363d',
            borderRadius: 16, padding: '32px 40px', textAlign: 'center',
            maxWidth: 360,
          }}>
            <div style={{ fontSize: '2rem', marginBottom: 12 }}>🪙</div>
            <div style={{ fontSize: '1.1rem', fontWeight: 700, color: '#e2e8f0', marginBottom: 8 }}>
              Out of chips!
            </div>
            <div style={{ fontSize: '0.85rem', color: '#8b949e', marginBottom: 24 }}>
              Get $1,000 in chips to keep playing.
            </div>
            <div style={{ display: 'flex', gap: 10, justifyContent: 'center' }}>
              <button
                onClick={handleRestock}
                disabled={restocking}
                style={{
                  padding: '10px 24px',
                  background: 'linear-gradient(135deg, #1f6feb22, #38a16922)',
                  border: '1px solid #38a169', borderRadius: 8,
                  color: '#68d391', fontWeight: 700, cursor: 'pointer',
                  fontSize: '0.9rem',
                }}
              >
                {restocking ? 'Getting chips...' : 'Get $1,000'}
              </button>
              <button
                onClick={() => setShowRestock(false)}
                style={{
                  padding: '10px 24px', background: 'none',
                  border: '1px solid #30363d', borderRadius: 8,
                  color: '#8b949e', cursor: 'pointer', fontSize: '0.9rem',
                }}
              >
                Not now
              </button>
            </div>
          </div>
        </div>
      )}

      <SessionHistoryDrawer rounds={rounds} isDemo={isDemo} />
    </div>
  );
}

export default App;
