import React, { useState, useEffect, useRef } from 'react';
import { ObservabilityEvent } from '../types';

const GATEWAY_URL = import.meta.env.VITE_GATEWAY_URL || '';

const SERVICE_COLORS: Record<string, string> = {
  gateway: '#4a9eff',
  'game-state': '#38a169',
  'deck-service': '#d69e2e',
  'hand-evaluator': '#a855f7',
  'dealer-ai': '#84cc16',
  'bank-service': '#ef4444',
  'auth-service': '#0ea5e9',
  'chat-service': '#e879f9',
  'email-service': '#6b7280',
};

const PROTOCOL_BADGES: Record<string, { label: string; color: string }> = {
  sse: { label: 'SSE', color: '#38a169' },
  websocket: { label: 'WS', color: '#d69e2e' },
  http: { label: 'HTTP', color: '#4a9eff' },
  mtls: { label: 'mTLS', color: '#a855f7' },
};

interface EventRowProps {
  evt: ObservabilityEvent;
}

const EventRow: React.FC<EventRowProps> = ({ evt }) => {
  const callerColor = SERVICE_COLORS[evt.caller] || '#718096';
  const calleeColor = SERVICE_COLORS[evt.callee] || '#718096';
  const proto = PROTOCOL_BADGES[evt.protocol] || { label: evt.protocol, color: '#718096' };
  const statusColor = evt.statusCode >= 400 ? '#fc8181' : evt.statusCode >= 200 ? '#68d391' : '#a0aec0';

  return (
    <div style={{
      display: 'flex',
      alignItems: 'center',
      gap: 8,
      padding: '4px 0',
      borderBottom: '1px solid rgba(255,255,255,0.05)',
      fontSize: '0.7rem',
      fontFamily: 'monospace',
    }}>
      <span style={{ color: '#4a5568', minWidth: 60 }}>
        {new Date(evt.timestamp).toLocaleTimeString()}
      </span>
      <span style={{ color: callerColor, minWidth: 90 }}>{evt.caller}</span>
      <span style={{ color: '#4a5568' }}>→</span>
      <span style={{ color: calleeColor, minWidth: 100 }}>{evt.callee}</span>
      <span style={{
        background: `${proto.color}22`,
        color: proto.color,
        border: `1px solid ${proto.color}44`,
        borderRadius: 4,
        padding: '0 4px',
        fontSize: '0.6rem',
        fontWeight: 700,
        minWidth: 36,
        textAlign: 'center',
      }}>{proto.label}</span>
      <span style={{ color: '#a0aec0', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        {evt.method} {evt.path}
      </span>
      <span style={{ color: statusColor, minWidth: 32, textAlign: 'right' }}>{evt.statusCode}</span>
      <span style={{ color: '#4a5568', minWidth: 48, textAlign: 'right' }}>{evt.latencyMs}ms</span>
    </div>
  );
};

export const ObservabilityPanel: React.FC<{ compact?: boolean }> = ({ compact }) => {
  const [events, setEvents] = useState<ObservabilityEvent[]>([]);
  const [connected, setConnected] = useState(false);
  const [expanded, setExpanded] = useState(true);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const es = new EventSource(`${GATEWAY_URL}/events`);

    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);

    es.addEventListener('service_call', (evt: MessageEvent) => {
      try {
        const event: ObservabilityEvent = JSON.parse(evt.data);
        setEvents(prev => [...prev.slice(-99), event]); // keep last 100
      } catch (e) {
        console.error('obs parse error:', e);
      }
    });

    return () => es.close();
  }, []);

  useEffect(() => {
    if (expanded) {
      bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
    }
  }, [events, expanded]);

  return (
    <div style={{
      background: '#0d1117',
      border: '1px solid #21262d',
      borderRadius: 8,
      overflow: 'hidden',
    }}>
      {/* Header */}
      <div
        onClick={() => setExpanded(e => !e)}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          padding: '8px 12px',
          background: '#161b22',
          cursor: 'pointer',
          userSelect: 'none',
        }}
      >
        <div style={{
          width: 8,
          height: 8,
          borderRadius: '50%',
          background: connected ? '#38a169' : '#e53e3e',
          boxShadow: connected ? '0 0 6px #38a169' : undefined,
        }} />
        <span style={{ color: '#8b949e', fontSize: '0.7rem', letterSpacing: 2, textTransform: 'uppercase', flex: 1 }}>
          TCA Activity — {events.length} events
        </span>
        <span style={{ color: '#4a5568', fontSize: '0.7rem' }}>{expanded ? '▼' : '▶'}</span>
      </div>

      {/* Column headers */}
      {expanded && (
        <>
          <div style={{
            display: 'flex',
            gap: 8,
            padding: '4px 8px',
            background: '#0d1117',
            fontSize: '0.6rem',
            color: '#4a5568',
            fontFamily: 'monospace',
            letterSpacing: 1,
            textTransform: 'uppercase',
            borderBottom: '1px solid #21262d',
          }}>
            <span style={{ minWidth: 60 }}>Time</span>
            <span style={{ minWidth: 90 }}>Caller</span>
            <span style={{ minWidth: 16 }}></span>
            <span style={{ minWidth: 100 }}>Callee</span>
            <span style={{ minWidth: 36 }}>Proto</span>
            <span style={{ flex: 1 }}>Path</span>
            <span style={{ minWidth: 32, textAlign: 'right' }}>Status</span>
            <span style={{ minWidth: 48, textAlign: 'right' }}>Latency</span>
          </div>

          {/* Events */}
          <div style={{
            height: 200,
            overflowY: 'auto',
            padding: '0 8px',
          }}>
            {events.length === 0 ? (
              <div style={{ color: '#4a5568', textAlign: 'center', padding: '20px 0', fontSize: '0.75rem' }}>
                Waiting for TCA activity...
              </div>
            ) : (
              events.map(evt => <EventRow key={evt.id} evt={evt} />)
            )}
            <div ref={bottomRef} />
          </div>
        </>
      )}
    </div>
  );
};
