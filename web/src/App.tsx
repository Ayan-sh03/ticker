import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { ApiError, date, price, request, useResource, type Event, type Session } from './api';
import { EventView } from './EventView';
import { ErrorMessage } from './Feedback';
import { Organizer } from './Organizer';
import { Signup } from './Signup';
import './organizer.css';

export function App() {
  const [eventID, setEventID] = useState(() => location.hash.slice(1).toLowerCase());
  const [selectedIDs, select] = useState<string[]>([]);
  const [session, setSession] = useState<Session | null>(null);
  const [checking, setChecking] = useState(true);
  const [accountError, setAccountError] = useState('');
  const [loggingOut, setLoggingOut] = useState(false);
  // The dialog shows login or signup; email carries an address over to login.
  const [auth, setAuth] = useState<{ mode: 'login' | 'signup'; email?: string }>({ mode: 'login' });
  const dialog = useRef<HTMLDialogElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const organizerRoute = eventID === 'organizer' || eventID.startsWith('organizer/');
  const sessionExpired = useCallback(() => { select([]); setSession(null); }, []);

  useEffect(() => {
    const change = () => { setEventID(location.hash.slice(1).toLowerCase()); select([]); };
    window.addEventListener('hashchange', change);
    return () => window.removeEventListener('hashchange', change);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    request<Session>('/me', { signal: controller.signal }).then(setSession).catch(error => {
      if (!controller.signal.aborted && !(error instanceof ApiError && error.status === 401)) {
        setAccountError('Unable to check your account. Reload to try again.');
      }
    }).finally(() => { if (!controller.signal.aborted) setChecking(false); });
    return () => controller.abort();
  }, []);

  function signedIn(value: Session) {
    setSession(value);
    setAccountError('');
    dialog.current?.close();
  }

  async function logout() {
    select([]);
    setLoggingOut(true);
    setAccountError('');
    try {
      await request<void>('/sessions/current', { method: 'DELETE', headers: { 'X-Ticker-User': session!.user.id } });
      setSession(null);
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 401) setSession(null);
      else setAccountError('Unable to log out. Please try again.');
    }
    finally { setLoggingOut(false); }
  }

  return <div className="shell">
    <a className="skip-link" href="#content" onClick={event => { event.preventDefault(); document.getElementById('content')?.focus(); }}>Skip to content</a>
    <header className="masthead">
      <a className="wordmark" href="#" aria-label="Ticker, all events">ticker<span aria-hidden="true">.</span></a>
      <div className="account">
        <nav className="app-nav" aria-label="Main navigation">
          <a href="#" aria-current={!organizerRoute ? 'page' : undefined}>Events</a>
          {!!session?.user.tenant_ids.length && !loggingOut && <a href="#organizer" aria-current={organizerRoute ? 'page' : undefined}>Organizer</a>}
        </nav>
        {session && <span className="account-name" title={session.user.display_name}>{session.user.display_name}</span>}
        <button className="button" disabled={checking || loggingOut} onClick={() => {
          if (session) void logout(); else dialog.current?.showModal();
        }}>{checking ? 'Checking…' : loggingOut ? 'Logging out…' : session ? 'Log out' : 'Log in'}</button>
      </div>
    </header>
    {accountError && <p className="error" role="alert">{accountError}</p>}
    <main className="content" id="content" tabIndex={-1}>
      {organizerRoute
        ? <div className="organizer"><Organizer key={`${eventID}:${loggingOut ? 'leaving' : session?.user.id ?? 'guest'}`} route={eventID}
          session={loggingOut ? null : session} checking={checking || loggingOut} heading={heading}
          onLogin={() => dialog.current?.showModal()} onSessionExpired={sessionExpired} /></div>
        : eventID && eventID !== 'content'
        ? <EventView key={`${eventID}:${loggingOut ? 'leaving' : session?.user.id ?? 'guest'}`} id={eventID} heading={heading}
          session={loggingOut ? null : session} checking={checking || loggingOut} selectedIDs={selectedIDs} select={select}
          onLogin={() => dialog.current?.showModal()} onSessionExpired={sessionExpired} />
        : <EventIndex heading={heading} />}
    </main>
    <footer className="footer"><span>Ticker</span><span>Choose your seat.</span></footer>
    <dialog ref={dialog} className="login-dialog" onClick={event => {
      if (event.target === dialog.current) {
        const box = dialog.current!.getBoundingClientRect();
        if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) dialog.current?.close();
      }
    }} onClose={() => setAuth({ mode: 'login' })} aria-labelledby="login-title">
      {auth.mode === 'signup'
        ? <Signup onSuccess={signedIn} onClose={() => dialog.current?.close()} onLogin={email => setAuth({ mode: 'login', email })} />
        : <Login key={auth.email ?? ''} email={auth.email} onSuccess={signedIn} onClose={() => dialog.current?.close()} onSignup={() => setAuth({ mode: 'signup' })} />}
    </dialog>
  </div>;
}

function EventIndex({ heading }: { heading: React.RefObject<HTMLHeadingElement | null> }) {
  const [offset, setOffset] = useState(0);
  const { data, error, retry } = useResource<{ events: Event[] }>(`/events?limit=20&offset=${offset}`);
  useEffect(() => { if (data) heading.current?.focus(); }, [data, heading]);
  return <>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Events</h1><p>Browse events and choose a seat.</p></div>
    {error && <ErrorMessage message={error} retry={retry} />}
    {!data && !error && <p className="loading" role="status">Loading events…</p>}
    {data && <>
      {data.events.length === 0 && <p>No events are listed here yet.</p>}
      <ul className="event-list">{data.events.map(event => <li key={event.id}>
        <div className="event-entry"><div><p className="eyebrow">{date(event)}</p><h2>{event.title}</h2><p>{event.venue_name}</p></div>
          <div className="event-action"><span className="price">{price(event)}<small>per seat</small></span><a className="button button-solid" href={`#${event.id}`} aria-label={`View seats for ${event.title}`}>View seats <span aria-hidden="true">↗</span></a></div>
        </div>
      </li>)}</ul>
    </>}
    {(offset > 0 || data?.events.length === 20) && <nav className="pagination" aria-label="Event pages">
      <button className="button" disabled={offset === 0} onClick={() => setOffset(value => value - 20)}>Previous</button>
      <button className="button" disabled={!data || data.events.length < 20} onClick={() => setOffset(value => value + 20)}>Next</button>
    </nav>}
  </>;
}

function Login({ email, onSuccess, onClose, onSignup }: {
  email?: string; onSuccess: (session: Session) => void; onClose: () => void; onSignup: () => void;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const formRef = useRef<HTMLFormElement>(null);
  useEffect(() => {
    const dialog = formRef.current?.closest('dialog');
    const reset = () => { formRef.current?.reset(); setError(''); };
    dialog?.addEventListener('close', reset);
    return () => dialog?.removeEventListener('close', reset);
  }, []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    const form = event.currentTarget;
    const fields = new FormData(form);
    setPending(true);
    setError('');
    try {
      const session = await request<Session>('/sessions', { method: 'POST', body: JSON.stringify({ email: fields.get('email'), password: fields.get('password') }) });
      form.reset();
      onSuccess(session);
    } catch (failure) {
      setError(failure instanceof ApiError && failure.status === 401
        ? 'Email or password is incorrect. Check both and try again.'
        : 'Unable to log in. Please try again.');
    } finally { setPending(false); }
  }
  return <form ref={formRef} onSubmit={submit} onInput={() => { if (error) setError(''); }} className="login-form">
    <h2 id="login-title">Welcome back.</h2><p>Log in to your account.</p>
    <label htmlFor="email">Email address</label><input id="email" name="email" type="email" maxLength={254} autoComplete="username" defaultValue={email} autoFocus={!email} required aria-invalid={!!error} aria-describedby="login-error" />
    <label htmlFor="password">Password</label><input id="password" name="password" type="password" autoComplete="current-password" autoFocus={!!email} required aria-invalid={!!error} aria-describedby="login-error" />
    <p id="login-error" className="error form-message" role="alert">{error}</p>
    <div className="form-actions"><button className="button button-solid" disabled={pending}>{pending ? 'Logging in…' : 'Log in'}</button><button type="button" className="button" disabled={pending} onClick={onClose}>Close</button></div>
    <p className="form-switch">New to Ticker? <button type="button" className="text-button" disabled={pending} onClick={onSignup}>Create an account</button></p>
  </form>;
}
