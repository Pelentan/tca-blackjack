import React, { useState } from 'react';
import { GameState, PlayerState, DealerState, PlayerAction, Card } from '../types';
import { CardComponent } from './Card';

interface GameTableProps {
  gameState:              GameState;
  onAction:               (action: PlayerAction, amount?: number) => void;
  myPlayerId?:            string;
  isDemo?:                boolean;
  authoritativeBalance?:  number | null; // from bank SSE — overrides game-state chips
}

const PHASE_LABELS: Record<string, string> = {
  waiting:     'Place your bet',
  betting:     'Placing bet...',
  dealing:     'Dealing...',
  player_turn: "Your turn",
  dealer_turn: "Dealer's turn",
  payout:      'Round complete',
  complete:    'Round complete',
};

const STATUS_COLORS: Record<string, string> = {
  waiting:   '#718096',
  betting:   '#d69e2e',
  playing:   '#3182ce',
  standing:  '#718096',
  bust:      '#e53e3e',
  blackjack: '#d4af37',
  won:       '#38a169',
  lost:      '#e53e3e',
  push:      '#718096',
};

const STATUS_LABELS: Record<string, string> = {
  blackjack: '🎉 BLACKJACK!',
  won:       '✓ WIN',
  lost:      '✗ LOST',
  push:      '= PUSH',
  bust:      '✗ BUST',
};

// Detect five-card Charlie from hand length + status
function getFiveCardLabel(hand: Card[], status: string): string | null {
  if (hand.length >= 5 && (status === 'won' || status === 'standing')) return '🖐 FIVE CARD CHARLIE!';
  return null;
}

const DealerView: React.FC<{ dealer: DealerState }> = ({ dealer }) => (
  <div style={{ textAlign: 'center', marginBottom: 24 }}>
    <div style={{ color: '#a0aec0', fontSize: '0.75rem', letterSpacing: 2, marginBottom: 8 }}>
      DEALER {dealer.isRevealed && dealer.handValue > 0 ? `— ${dealer.handValue}` : ''}
    </div>
    <div style={{ display: 'flex', gap: 8, justifyContent: 'center', flexWrap: 'wrap' }}>
      {dealer.hand.map((card, i) => (
        <CardComponent key={i} card={card} size="md" />
      ))}
    </div>
  </div>
);

const PlayerView: React.FC<{ player: PlayerState; isActive: boolean; isMe: boolean }> = ({
  player, isActive, isMe,
}) => (
  <div style={{
    background: isActive ? 'rgba(49,130,206,0.15)' : 'rgba(255,255,255,0.04)',
    border:     `1px solid ${isActive ? '#3182ce' : 'rgba(255,255,255,0.1)'}`,
    borderRadius: 12,
    padding:    '16px 20px',
    minWidth:   180,
    transition: 'all 0.3s',
  }}>
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
      <span style={{ fontWeight: 600, color: isMe ? '#63b3ed' : '#e2e8f0', fontSize: '0.9rem' }}>
        {player.name} {isMe ? '(you)' : ''}
      </span>
      <span style={{
        fontSize: '0.65rem', fontWeight: 700,
        color: STATUS_COLORS[player.status] || '#718096',
        textTransform: 'uppercase', letterSpacing: 1,
      }}>
        {getFiveCardLabel(player.hand, player.status) ?? STATUS_LABELS[player.status] ?? player.status}
      </span>
    </div>

    <div style={{ display: 'flex', gap: 6, marginBottom: 10, flexWrap: 'wrap' }}>
      {player.hand.map((card, i) => (
        <CardComponent key={i} card={card} size="sm" />
      ))}
    </div>

    {player.handValue > 0 && (
      <div style={{ fontSize: '0.8rem', color: '#a0aec0', marginBottom: 4 }}>
        Hand: <strong style={{ color: player.handValue > 21 ? '#fc8181' : '#e2e8f0' }}>
          {player.handValue}
        </strong>
        {player.isSoftHand && <span style={{ color: '#a0aec0' }}> (soft)</span>}
      </div>
    )}

    {player.currentBet > 0 && (
      <div style={{ fontSize: '0.8rem', color: '#a0aec0' }}>
        Bet: <strong style={{ color: '#d69e2e' }}>${player.currentBet}</strong>
      </div>
    )}
  </div>
);

// Bet panel for real player games
const BetPanel: React.FC<{
  chips:    number;
  minBet:   number;
  maxBet:   number;
  onBet:    (amount: number) => void;
}> = ({ chips, minBet, maxBet, onBet }) => {
  const [betAmount, setBetAmount] = useState<number>(Math.min(25, maxBet));

  const quickAmounts = [10, 25, 50, 100, 200].filter(a => a >= minBet && a <= chips);
  const canBet = betAmount >= minBet && betAmount <= Math.min(maxBet, chips);

  return (
    <div style={{ textAlign: 'center' }}>
      <div style={{ color: '#d69e2e', fontSize: '0.75rem', letterSpacing: 1, marginBottom: 12 }}>
        PLACE YOUR BET (min ${minBet} · max ${maxBet})
      </div>

      {/* Quick chips */}
      <div style={{ display: 'flex', gap: 8, justifyContent: 'center', marginBottom: 10, flexWrap: 'wrap' }}>
        {quickAmounts.map(amount => (
          <button
            key={amount}
            onClick={() => setBetAmount(amount)}
            style={{
              padding: '6px 14px',
              borderRadius: 8,
              border: `1px solid ${betAmount === amount ? '#d69e2e' : '#4a4a2a'}`,
              background: betAmount === amount ? '#d69e2e33' : 'transparent',
              color: betAmount === amount ? '#d69e2e' : '#8b7a3a',
              fontWeight: 700,
              cursor: 'pointer',
              fontSize: '0.85rem',
              transition: 'all 0.15s',
            }}
          >
            ${amount}
          </button>
        ))}
        {chips > 0 && (
          <button
            onClick={() => setBetAmount(Math.min(chips, maxBet))}
            style={{
              padding: '6px 14px',
              borderRadius: 8,
              border: `1px solid ${betAmount === Math.min(chips, maxBet) ? '#d69e2e' : '#4a4a2a'}`,
              background: betAmount === Math.min(chips, maxBet) ? '#d69e2e33' : 'transparent',
              color: betAmount === Math.min(chips, maxBet) ? '#d69e2e' : '#8b7a3a',
              fontWeight: 700,
              cursor: 'pointer',
              fontSize: '0.85rem',
            }}
          >
            All-In
          </button>
        )}
      </div>

      {/* Custom amount */}
      <div style={{ display: 'flex', gap: 8, justifyContent: 'center', alignItems: 'center', marginBottom: 14 }}>
        <input
          type="number"
          min={minBet}
          max={Math.min(maxBet, chips)}
          value={betAmount}
          onChange={e => setBetAmount(Math.max(0, parseInt(e.target.value) || 0))}
          style={{
            width: 90,
            padding: '6px 10px',
            background: '#1a2535',
            border: '1px solid #30363d',
            borderRadius: 6,
            color: '#e2e8f0',
            fontSize: '0.9rem',
            textAlign: 'center',
          }}
        />
        <span style={{ color: '#8b949e', fontSize: '0.8rem' }}>chips</span>
      </div>

      <button
        onClick={() => canBet && onBet(betAmount)}
        disabled={!canBet}
        style={{
          padding: '10px 32px',
          borderRadius: 8,
          border: '1px solid #d69e2e',
          background: canBet ? 'linear-gradient(135deg, #d69e2e33, #d69e2e22)' : 'transparent',
          color: canBet ? '#d69e2e' : '#4a4535',
          fontWeight: 700,
          cursor: canBet ? 'pointer' : 'not-allowed',
          fontSize: '1rem',
          letterSpacing: 1,
          transition: 'all 0.2s',
        }}
      >
        Deal →
      </button>
    </div>
  );
};

export const GameTable: React.FC<GameTableProps> = ({
  gameState,
  onAction,
  myPlayerId,
  isDemo = false,
  authoritativeBalance = null,
}) => {
  // In a real (non-demo) single-player table, always treat players[0] as "me"
  // regardless of ID — guards against ID mismatch if table was created with stale state.
  const myPlayer  = isDemo
    ? gameState.players[0]
    : (gameState.players.find(p => p.id === myPlayerId) ?? gameState.players[0]);
  const isMyTurn  = isDemo
    ? !!gameState.activePlayerId
    : (gameState.activePlayerId === myPlayerId || gameState.activePlayerId === myPlayer?.id);
  const phase     = gameState.phase;
  const canDouble = myPlayer ? myPlayer.chips >= myPlayer.currentBet : false;

  // Pair detection for split stub
  const hasPair = myPlayer?.hand?.length === 2 &&
    myPlayer.hand[0]?.rank === myPlayer.hand[1]?.rank;

  return (
    <div style={{
      background:   'radial-gradient(ellipse at center, #1a4731 0%, #0d2818 100%)',
      borderRadius: 24,
      padding:      '32px 24px',
      border:       '3px solid rgba(255,255,255,0.1)',
      boxShadow:    'inset 0 0 60px rgba(0,0,0,0.5)',
      position:     'relative',
    }}>
      {/* Phase indicator */}
      <div style={{
        textAlign: 'center', marginBottom: 24,
        padding: '6px 16px', background: 'rgba(0,0,0,0.3)',
        borderRadius: 20, display: 'inline-block',
        left: '50%', position: 'relative', transform: 'translateX(-50%)',
      }}>
        <span style={{ color: '#d69e2e', fontSize: '0.8rem', fontWeight: 600, letterSpacing: 2, textTransform: 'uppercase' }}>
          {isDemo ? (PHASE_LABELS[phase] || phase).replace('Your turn', "Player's turn").replace('Place your bet', 'Waiting...') : (PHASE_LABELS[phase] || phase)}
        </span>
      </div>

      {/* Service attribution */}
      <div style={{
        position: 'absolute', top: 12, right: 16,
        fontSize: '0.6rem', color: 'rgba(255,255,255,0.3)', fontFamily: 'monospace',
      }}>
        {gameState.handledBy}
      </div>

      {/* Demo badge */}
      {isDemo && (
        <div style={{
          position: 'absolute', top: 12, left: 16,
          fontSize: '0.6rem', color: '#f59e0b', fontWeight: 700,
          letterSpacing: 1, background: '#f59e0b22',
          padding: '2px 8px', borderRadius: 4, border: '1px solid #f59e0b44',
        }}>
          DEMO
        </div>
      )}

      {/* Dealer */}
      <DealerView dealer={gameState.dealer} />

      <div style={{ borderTop: '1px dashed rgba(255,255,255,0.15)', margin: '16px 0' }} />

      {/* Players */}
      <div style={{ display: 'flex', gap: 12, justifyContent: 'center', flexWrap: 'wrap', marginBottom: 24 }}>
        {gameState.players.map(player => (
          <PlayerView
            key={player.id}
            player={player}
            isActive={gameState.activePlayerId === player.id}
            isMe={player.id === myPlayerId}
          />
        ))}
      </div>

      {/* === Player game action area === */}
      {!isDemo && (
        <>
          {/* Betting phase */}
          {phase === 'waiting' && myPlayer && (
            <BetPanel
              chips={authoritativeBalance !== null ? authoritativeBalance : myPlayer.chips}
              minBet={gameState.minBet}
              maxBet={gameState.maxBet}
              onBet={amount => onAction('bet', amount)}
            />
          )}

          {/* Player turn actions */}
          {isMyTurn && myPlayer?.status === 'playing' && (
            <div style={{ display: 'flex', gap: 8, justifyContent: 'center', flexWrap: 'wrap' }}>
              {/* Hit */}
              <ActionButton label="Hit" color="#3182ce" onClick={() => onAction('hit')} />

              {/* Stand */}
              <ActionButton label="Stand" color="#718096" onClick={() => onAction('stand')} />

              {/* Double */}
              <ActionButton
                label="Double"
                color="#d69e2e"
                onClick={() => onAction('double')}
                disabled={!canDouble}
                disabledTitle={!canDouble ? "Insufficient chips to double" : undefined}
              />

              {/* Split — stubbed */}
              <ActionButton
                label="Split"
                color="#8b949e"
                onClick={() => {}}
                disabled={true}
                disabledTitle={hasPair ? "Split coming soon" : "Split requires a pair"}
              />
            </div>
          )}
        </>
      )}
    </div>
  );
};

const ActionButton: React.FC<{
  label:         string;
  color:         string;
  onClick:       () => void;
  disabled?:     boolean;
  disabledTitle?: string;
}> = ({ label, color, onClick, disabled, disabledTitle }) => (
  <button
    onClick={disabled ? undefined : onClick}
    title={disabled ? disabledTitle : undefined}
    style={{
      padding:      '10px 28px',
      borderRadius: 8,
      border:       `1px solid ${disabled ? '#333' : color}`,
      background:   disabled ? 'transparent' : `${color}22`,
      color:        disabled ? '#4a5568' : 'white',
      fontWeight:   600,
      cursor:       disabled ? 'not-allowed' : 'pointer',
      fontSize:     '0.9rem',
      transition:   'all 0.2s',
      opacity:      disabled ? 0.5 : 1,
    }}
  >
    {label}
  </button>
);
