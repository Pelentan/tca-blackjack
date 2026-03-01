import React, { useState } from 'react';
import { startRegistration } from '@simplewebauthn/browser';

const GATEWAY_URL = import.meta.env.VITE_GATEWAY_URL || '';
const AUTH_UI     = `${GATEWAY_URL}/api/auth-ui`;

export interface EnrollmentResult {
  accessToken: string;
  expiresIn:   number;
  playerId:    string;
  playerName:  string;
  email:       string;
}

interface Props {
  bootstrapToken: string;
  playerId:       string;
  playerName:     string;
  email:          string;
  onSuccess:      (result: EnrollmentResult) => void;
}

type EnrollState = 'prompt' | 'enrolling' | 'success' | 'error';

export const EnrollmentModal: React.FC<Props> = ({
  bootstrapToken, playerId, playerName, email, onSuccess,
}) => {
  const [state, setState]   = useState<EnrollState>('prompt');
  const [errMsg, setErrMsg] = useState<string | null>(null);

  const handleEnroll = async () => {
    setState('enrolling');
    setErrMsg(null);

    try {
      // 1. Begin — get registration options (requires bootstrap JWT)
      const beginRes = await fetch(`${AUTH_UI}/passkey/register/begin`, {
        method:  'POST',
        headers: {
          'Content-Type':  'application/json',
          'Authorization': `Bearer ${bootstrapToken}`,
        },
        body: JSON.stringify({}),
      });

      if (!beginRes.ok) {
        const err = await beginRes.json();
        throw new Error(err.error ?? 'Failed to start passkey registration');
      }

      const options = await beginRes.json();

      // 2. Browser ceremony — OS handles biometric/PIN prompt
      const credential = await startRegistration({ optionsJSON: options });

      // 3. Complete — verify attestation, issues session JWT
      const completeRes = await fetch(`${AUTH_UI}/passkey/register/complete`, {
        method:  'POST',
        headers: {
          'Content-Type':  'application/json',
          'Authorization': `Bearer ${bootstrapToken}`,
        },
        body: JSON.stringify(credential),
      });

      if (!completeRes.ok) {
        const err = await completeRes.json();
        throw new Error(err.error ?? 'Passkey enrollment failed');
      }

      const result = await completeRes.json() as EnrollmentResult;

      setState('success');
      // Brief pause so the success state is visible before the modal closes
      setTimeout(() => onSuccess(result), 800);

    } catch (e: any) {
      if (e.name === 'NotAllowedError') {
        // User cancelled or dismissed the browser dialog
        setErrMsg('Passkey enrollment is required to use TCA Blackjack. Please try again.');
      } else {
        setErrMsg(e.message ?? 'Enrollment failed — please try again');
      }
      setState('error');
    }
  };

  return (
    <div style={backdropStyle}>
      <div style={modalStyle}>

        {state === 'success' ? (
          <div style={{ textAlign: 'center', padding: '8px 0' }}>
            <div style={{ fontSize: '3rem', marginBottom: 16 }}>✓</div>
            <h2 style={{ color: '#68d391', margin: '0 0 8px', fontSize: '1.2rem' }}>
              Passkey registered
            </h2>
            <p style={{ color: '#8b949e', margin: 0, fontSize: '0.85rem' }}>
              Signing you in…
            </p>
          </div>
        ) : state === 'enrolling' ? (
          <div style={{ textAlign: 'center', padding: '8px 0' }}>
            <div style={{ fontSize: '2.5rem', marginBottom: 16 }}>🔑</div>
            <h2 style={{ color: '#e6edf3', margin: '0 0 12px', fontSize: '1.2rem' }}>
              Waiting for passkey
            </h2>
            <p style={{ color: '#8b949e', margin: 0, lineHeight: 1.6, fontSize: '0.88rem' }}>
              Approve the passkey prompt on your device to continue.
            </p>
          </div>
        ) : (
          <>
            {/* Header */}
            <div style={{ textAlign: 'center', marginBottom: 24 }}>
              <div style={{ fontSize: '2.5rem', marginBottom: 12 }}>🔐</div>
              <h2 style={{ color: '#e6edf3', margin: '0 0 8px', fontSize: '1.25rem' }}>
                One more step, {playerName}
              </h2>
              <p style={{ color: '#8b949e', margin: 0, fontSize: '0.82rem', lineHeight: 1.6 }}>
                Email verified as <strong style={{ color: '#58a6ff' }}>{email}</strong>
              </p>
            </div>

            {/* Explanation */}
            <div style={{
              background: '#0d1a2e', border: '1px solid #1f4a7a', borderRadius: 8,
              padding: '14px 16px', marginBottom: 20,
            }}>
              <div style={{ color: '#7ab3e0', fontSize: '0.8rem', fontWeight: 600, marginBottom: 8 }}>
                Why passkeys?
              </div>
              <div style={{ color: '#8b949e', fontSize: '0.78rem', lineHeight: 1.6 }}>
                TCA Blackjack uses passkeys instead of passwords — your device's biometric
                (fingerprint, face, or PIN) is the key. No password to steal, no magic links to wait for.
                Once registered, signing in takes one tap.
              </div>
            </div>

            {/* Security badge */}
            <div style={{
              display: 'flex', gap: 12, marginBottom: 20, flexWrap: 'wrap',
            }}>
              {['Phishing-resistant', 'Device-bound', 'No password'].map(label => (
                <div key={label} style={{
                  fontSize: '0.68rem', padding: '3px 10px',
                  background: '#0d2b1a', border: '1px solid #2d6a4f',
                  borderRadius: 20, color: '#52b788',
                }}>
                  ✓ {label}
                </div>
              ))}
            </div>

            {/* Error */}
            {state === 'error' && errMsg && (
              <div style={{
                background: '#2d1515', border: '1px solid #e53e3e', borderRadius: 8,
                padding: '10px 14px', color: '#fc8181', fontSize: '0.8rem', marginBottom: 16,
              }}>
                {errMsg}
              </div>
            )}

            {/* CTA — no dismiss, no skip */}
            <button
              onClick={handleEnroll}
              style={{
                display: 'block', width: '100%', padding: '12px 0',
                background: '#1f6feb', border: '1px solid #1f6feb',
                borderRadius: 8, color: '#fff', fontWeight: 700,
                fontSize: '0.95rem', cursor: 'pointer', letterSpacing: 0.5,
              }}
            >
              Register Passkey
            </button>

            <p style={{
              textAlign: 'center', color: '#4a5568', fontSize: '0.7rem',
              marginTop: 12, marginBottom: 0,
            }}>
              This step is required. Your account is inactive until a passkey is registered.
            </p>
          </>
        )}
      </div>
    </div>
  );
};

const backdropStyle: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.85)',
  display: 'flex', alignItems: 'center', justifyContent: 'center',
  zIndex: 1000, backdropFilter: 'blur(6px)',
};

const modalStyle: React.CSSProperties = {
  background: '#161b22', border: '1px solid #30363d', borderRadius: 16,
  padding: '32px', width: '100%', maxWidth: 420,
  boxShadow: '0 24px 48px rgba(0,0,0,0.7)',
};
