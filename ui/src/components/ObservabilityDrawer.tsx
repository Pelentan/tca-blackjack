import React, { useState } from 'react';
import { ObservabilityPanel } from './ObservabilityPanel';

const LEGEND = [
  { name: 'Gateway',    lang: 'Go',         color: '#4a9eff' },
  { name: 'Game State', lang: 'Go',         color: '#38a169' },
  { name: 'Deck',       lang: 'Go',         color: '#d69e2e' },
  { name: 'Hand Eval',  lang: 'Haskell',    color: '#a855f7' },
  { name: 'Dealer AI',  lang: 'Python',     color: '#84cc16' },
  { name: 'Bank',       lang: 'COBOL+Go',   color: '#ef4444' },
  { name: 'Auth',       lang: 'TypeScript', color: '#0ea5e9' },
  { name: 'Chat',       lang: 'Elixir',     color: '#e879f9' },
];

const DRAWER_HEIGHT = 280;
const HEADER_HEIGHT = 34; // keep this much peeking above the bottom edge when closed

export const ObservabilityDrawer: React.FC = () => {
  const [open, setOpen] = useState(false);

  return (
    <>
      {/* Bottom drawer */}
      <div style={{
        position: 'fixed',
        left: 0,
        right: 0,
        bottom: open ? 0 : -(DRAWER_HEIGHT - HEADER_HEIGHT),
        height: DRAWER_HEIGHT,
        background: '#0d1117',
        borderTop: '1px solid #21262d',
        zIndex: 199,
        transition: 'bottom 0.25s ease',
        boxShadow: open ? '0 -4px 24px rgba(0,0,0,0.6)' : 'none',
        display: 'flex',
        flexDirection: 'column',
      }}>
        {/* Drag handle / header bar */}
        <div
          onClick={() => setOpen(o => !o)}
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '6px 16px',
            borderBottom: '1px solid #21262d',
            cursor: 'pointer',
            userSelect: 'none',
            background: '#161b22',
            flexShrink: 0,
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <span style={{
              fontSize: '0.65rem', color: '#8b949e',
              letterSpacing: 2, textTransform: 'uppercase',
            }}>
              Swarm Activity
            </span>
            {/* Legend inline in header */}
            <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
              {LEGEND.map(({ name, lang, color }) => (
                <div key={name} style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  <div style={{ width: 8, height: 8, borderRadius: 2, background: color }} />
                  <span style={{ fontSize: '0.62rem', color: '#8b949e' }}>{name}</span>
                  <span style={{ fontSize: '0.58rem', color: '#4a5568' }}>({lang})</span>
                </div>
              ))}
            </div>
          </div>
          <span style={{ color: '#4a5568', fontSize: '0.7rem' }}>
            {open ? '▼ close' : '▲ swarm activity'}
          </span>
        </div>

        {/* Event feed */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '0 12px' }}>
          <ObservabilityPanel compact />
        </div>
      </div>

      {/* Spacer so page content doesn't hide behind closed drawer tab */}
      <div style={{ height: 32 }} />

      {/* Backdrop — only when open, click to close */}
      {open && (
        <div
          onClick={() => setOpen(false)}
          style={{
            position: 'fixed', inset: 0,
            zIndex: 198, cursor: 'pointer',
          }}
        />
      )}
    </>
  );
};
