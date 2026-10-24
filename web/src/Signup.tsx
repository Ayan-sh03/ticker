import { useEffect, useState, type FormEvent } from 'react';
import { ApiError, request, type Session } from './api';

// API messages are short clauses; show them as sentences.
function sentence(failure: unknown, fallback: string) {
  if (!(failure instanceof ApiError) || failure.status >= 500 && failure.code !== 'signup_unavailable') return fallback;
  const text = failure.message.charAt(0).toUpperCase() + failure.message.slice(1);
  return /[.!?]$/.test(text) ? text : `${text}.`;
}

export function Signup({ onSuccess, onClose, onLogin }: {
  onSuccess: (session: Session) => void; onClose: () => void; onLogin: (email?: string) => void;
}) {
  // The address a code was sent to; empty until the first code is sent.
  const [email, setEmail] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [exists, setExists] = useState(false);
  const [resendAt, setResendAt] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const wait = Math.ceil((resendAt - now) / 1000);
  useEffect(() => {
    if (wait <= 0) return;
    const timer = setTimeout(() => setNow(Date.now()), 1000);
    return () => clearTimeout(timer);
  }, [wait, now]);

  async function sendCode(address: string) {
    if (pending) return;
    setPending(true);
    setError('');
    setExists(false);
    try {
      const sent = await request<{ resend_after_seconds: number }>('/signups/code', { method: 'POST', body: JSON.stringify({ email: address }) });
      const time = Date.now();
      setEmail(address);
      setNow(time);
      setResendAt(time + sent.resend_after_seconds * 1000);
    } catch (failure) {
      setError(sentence(failure, 'Unable to send a code. Please try again.'));
    } finally { setPending(false); }
  }

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    const fields = new FormData(event.currentTarget);
    setPending(true);
    setError('');
    setExists(false);
    try {
      const session = await request<Session>('/signups', { method: 'POST', body: JSON.stringify({
        email, code: fields.get('code'), display_name: fields.get('display_name'), password: fields.get('password'),
      }) });
      onSuccess(session);
    } catch (failure) {
      setExists(failure instanceof ApiError && failure.code === 'account_exists');
      setError(sentence(failure, 'Unable to create your account. Please try again.'));
    } finally { setPending(false); }
  }

  // Separate keys stop React reusing the email input as the code input.
  if (!email) return <form key="email" className="login-form" onInput={() => { if (error) setError(''); }} onSubmit={event => {
    event.preventDefault();
    void sendCode(String(new FormData(event.currentTarget).get('email')).trim());
  }}>
    <h2 id="login-title">Create an account.</h2><p>We'll email you a 6-digit code to confirm the address is yours.</p>
    <label htmlFor="signup-email">Email address</label><input id="signup-email" name="email" type="email" maxLength={254} autoComplete="email" autoFocus required aria-invalid={!!error} aria-describedby="signup-error" />
    <p id="signup-error" className="error form-message" role="alert">{error}</p>
    <div className="form-actions"><button className="button button-solid" disabled={pending}>{pending ? 'Sending…' : 'Send code'}</button><button type="button" className="button" disabled={pending} onClick={onClose}>Close</button></div>
    <p className="form-switch">Already have an account? <button type="button" className="text-button" disabled={pending} onClick={() => onLogin()}>Log in</button></p>
  </form>;

  return <form key="code" className="login-form" onSubmit={create} onInput={() => { if (error) { setError(''); setExists(false); } }}>
    <h2 id="login-title">Check your email.</h2><p>Enter the code sent to <strong>{email}</strong>. It expires in 10 minutes.</p>
    <label htmlFor="signup-code">Code</label><input id="signup-code" name="code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} autoFocus required aria-describedby="signup-error" />
    <label htmlFor="signup-name">Your name</label><input id="signup-name" name="display_name" autoComplete="name" maxLength={80} required />
    <label htmlFor="signup-password">Password</label><input id="signup-password" name="password" type="password" autoComplete="new-password" minLength={12} maxLength={72} required aria-describedby="signup-password-hint" />
    <p id="signup-password-hint" className="field-hint">12 to 72 characters.</p>
    <p id="signup-error" className="error form-message" role="alert">{error}{exists && <> <button type="button" className="text-button" onClick={() => onLogin(email)}>Log in</button></>}</p>
    <div className="form-actions"><button className="button button-solid" disabled={pending}>{pending ? 'Creating account…' : 'Create account'}</button><button type="button" className="button" disabled={pending} onClick={onClose}>Close</button></div>
    <p className="form-switch">
      <button type="button" className="text-button" disabled={pending || wait > 0} onClick={() => void sendCode(email)}>{wait > 0 ? `Send a new code in ${wait}s` : 'Send a new code'}</button>
      <span aria-hidden="true"> · </span>
      <button type="button" className="text-button" disabled={pending} onClick={() => { setEmail(''); setError(''); setExists(false); }}>Change email</button>
    </p>
  </form>;
}
