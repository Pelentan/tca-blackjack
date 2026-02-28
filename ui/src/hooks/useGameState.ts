import { useState, useEffect, useCallback, useRef } from 'react';
import { GameState, SSEGameEvent, PlayerAction, RoundSnapshot } from '../types';

const GATEWAY_URL = import.meta.env.VITE_GATEWAY_URL || '';
export const DEMO_TABLE_ID = 'demo-table-00000000-0000-0000-0000-000000000001';
export const DEMO_PLAYER_ID = 'player-00000000-0000-0000-0000-000000000001';

interface UseGameStateConfig {
  tableId?: string;
  playerId?: string;
  token?: string;
}

interface UseGameStateReturn {
  gameState: GameState | null;
  connected: boolean;
  error: string | null;
  rounds: RoundSnapshot[];
  sendAction: (action: PlayerAction, amount?: number) => Promise<void>;
}

export function useGameState(config: UseGameStateConfig = {}): UseGameStateReturn {
  const tableId  = config.tableId  ?? DEMO_TABLE_ID;
  const playerId = config.playerId ?? DEMO_PLAYER_ID;
  const token    = config.token;

  const [gameState, setGameState] = useState<GameState | null>(null);
  const [connected, setConnected] = useState(false);
  const [error, setError]         = useState<string | null>(null);
  const [rounds, setRounds]       = useState<RoundSnapshot[]>([]);
  const lastPhaseRef              = useRef<string | null>(null);

  // Single effect — reset and reconnect together when tableId changes
  useEffect(() => {
    // Reset state immediately on table switch
    setGameState(null);
    setConnected(false);
    setError(null);
    setRounds([]);
    lastPhaseRef.current = null;

    const url = `${GATEWAY_URL}/api/game/${tableId}/stream`;
    console.log(`[useGameState] Connecting SSE: ${url}`);

    const es = new EventSource(url);

    es.onopen = () => {
      console.log(`[useGameState] SSE open: ${tableId}`);
      setConnected(true);
      setError(null);
    };

    es.addEventListener('game_state', (evt: MessageEvent) => {
      try {
        const event: SSEGameEvent = JSON.parse(evt.data);
        const gs = event.data;
        setGameState(gs);
        setConnected(true);

        if (gs.phase === 'payout' && lastPhaseRef.current !== 'payout') {
          const snapshot: RoundSnapshot = {
            id:        `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
            timestamp: gs.timestamp,
            players:   gs.players,
            dealer:    gs.dealer,
          };
          setRounds(prev => [snapshot, ...prev].slice(0, 20));
        }
        lastPhaseRef.current = gs.phase;
      } catch (e) {
        console.error('[useGameState] parse error:', e);
      }
    });

    es.onerror = (evt) => {
      console.error('[useGameState] SSE error:', evt);
      setConnected(false);
      setError('Connection lost — reconnecting...');
    };

    return () => {
      console.log(`[useGameState] Closing SSE: ${tableId}`);
      es.close();
    };
  }, [tableId]);

  const sendAction = useCallback(async (action: PlayerAction, amount?: number) => {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (token && token.length > 10) headers['Authorization'] = `Bearer ${token}`;
    try {
      const response = await fetch(`${GATEWAY_URL}/api/game/${tableId}/action`, {
        method: 'POST',
        headers,
        body: JSON.stringify({ playerId, action, amount }),
      });
      if (!response.ok && response.status !== 202) {
        const err = await response.json().catch(() => ({}));
        throw new Error(err.message || `Action failed: ${response.status}`);
      }
    } catch (e) {
      console.error('[useGameState] action error:', e);
      setError(e instanceof Error ? e.message : 'Action failed');
      setTimeout(() => setError(null), 3000);
    }
  }, [tableId, playerId, token]);

  return { gameState, connected, error, rounds, sendAction };
}
