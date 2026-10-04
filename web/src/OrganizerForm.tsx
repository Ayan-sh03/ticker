import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ApiError, request, type DraftEventInput, type OrganizerEvent } from './api';
import { OrganizerEventFields, draftPayload, type Fields } from './OrganizerEventFields';

type Marker = { fields: Fields; payload: DraftEventInput; event_id?: string };
type Recovery = { kind: 'none' | 'pending' | 'saved' | 'storage'; marker?: Marker };
const emptyFields: Fields = { title: '', description: '', venue: '', amount: '', currency: 'INR', sales: '', starts: '', rows: '', columns: '' };
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function readMarker(key: string): Recovery {
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return { kind: 'none' };
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== 'object' || !('fields' in value) || !('payload' in value)) return { kind: 'pending' };
    const marker = value as Marker;
    if (!marker.fields || !Object.keys(emptyFields).every(field => typeof marker.fields[field as keyof Fields] === 'string')) return { kind: 'pending' };
    return { kind: typeof marker.event_id === 'string' && uuid.test(marker.event_id) ? 'saved' : 'pending', marker };
  } catch { return { kind: 'storage' }; }
}

export function OrganizerForm({ userID, tenantID, onSessionExpired }: { userID: string; tenantID: string; onSessionExpired: () => void }) {
  // A draft POST is not idempotent. Persist an account/workspace-scoped intent
  // before sending so a timeout or reload cannot silently resend it.
  const storageKey = `ticker:organizer-draft:${userID}:${tenantID}`;
  const [initial] = useState(() => readMarker(storageKey));
  const [recovery, setRecovery] = useState<Recovery>(initial);
  const [fields, setFields] = useState<Fields>(initial.marker?.fields ?? emptyFields);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [forbidden, setForbidden] = useState(false);
  const busy = useRef(false);
  const mounted = useRef(true);
  const submission = useRef<AbortController | null>(null);
  const listLink = `#organizer/${tenantID}`;
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; submission.current?.abort(); }; }, []);

  function update(field: keyof Fields, value: string) { setFields(previous => ({ ...previous, [field]: value })); setError(''); }
  function acknowledge() {
    try {
      sessionStorage.removeItem(storageKey);
      if (sessionStorage.getItem(storageKey) !== null) throw new Error('Storage unavailable');
      if (recovery.kind === 'saved') setFields(emptyFields);
      setRecovery({ kind: 'none' }); setError('');
    } catch { setRecovery({ kind: 'storage' }); }
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy.current || recovery.kind !== 'none' || forbidden) return;
    let payload: DraftEventInput;
    try { payload = draftPayload(fields); } catch (failure) { setError((failure as Error).message); return; }
    const marker: Marker = { fields, payload };
    try {
      const existing = readMarker(storageKey);
      if (existing.kind !== 'none') { setRecovery(existing); return; }
      const serialized = JSON.stringify(marker);
      sessionStorage.setItem(storageKey, serialized);
      if (sessionStorage.getItem(storageKey) !== serialized) throw new Error('Storage unavailable');
    } catch { setRecovery({ kind: 'storage' }); return; }
    busy.current = true;
    const controller = new AbortController();
    submission.current = controller;
    setRecovery({ kind: 'pending', marker }); setPending(true); setError('');
    try {
      const result = await request<{ event: OrganizerEvent }>(`/organizer/tenants/${tenantID}/events`, {
        method: 'POST', signal: controller.signal, headers: { 'X-Ticker-User': userID }, body: JSON.stringify(payload),
      });
      if (!result?.event || !uuid.test(result.event.id) || result.event.tenant_id !== tenantID || result.event.status !== 'DRAFT') throw new Error('Unconfirmed draft');
      const saved = { ...marker, event_id: result.event.id };
      try { sessionStorage.setItem(storageKey, JSON.stringify(saved)); } catch { /* The original intent still blocks an unsafe retry. */ }
      if (mounted.current) {
        setRecovery({ kind: 'saved', marker: saved });
        location.hash = `organizer/${tenantID}/${result.event.id}`;
      }
    } catch (failure) {
      // Even after navigation, retain unknown outcomes and clear only definite
      // rejected requests. This is also safe across account changes.
      const rejected = failure instanceof ApiError && failure.status >= 400 && failure.status < 500;
      if (rejected) {
        let cleared = false;
        try { sessionStorage.removeItem(storageKey); cleared = sessionStorage.getItem(storageKey) === null; } catch { /* Fail closed. */ }
        if (!mounted.current) return;
        setRecovery({ kind: cleared ? 'none' : 'storage' });
        if (failure.status === 401) onSessionExpired();
        else if (failure.status === 403) setForbidden(true);
        else setError(failure.message);
      } else if (mounted.current) {
        setRecovery({ kind: 'pending', marker });
        setError('The response could not be confirmed.');
      }
    } finally { busy.current = false; if (mounted.current) setPending(false); }
  }
  if (forbidden) return <div className="organizer-empty"><p className="error" role="alert">You no longer have access to this organizer workspace.</p><a className="button" href="#organizer">Recheck workspaces</a></div>;
  const blocked = recovery.kind !== 'none';
  return <>
    {!pending && blocked && <div className="organizer-recovery" role="status">
      {recovery.kind === 'saved' ? <>
        <p>Your draft was saved. Review it before publishing.</p>
        <div className="form-actions"><a className="button button-solid" href={`#organizer/${tenantID}/${recovery.marker!.event_id}`}>Review saved draft</a><button className="button" onClick={acknowledge}>Create another draft</button></div>
      </> : recovery.kind === 'storage' ? <>
        <p>Draft recovery storage is unavailable. Enable session storage in this browser and recheck before saving.</p>
        <div className="form-actions"><a className="button" href={listLink}>Check your events</a><button className="button" onClick={() => { const latest = readMarker(storageKey); setRecovery(latest); if (latest.marker) setFields(latest.marker.fields); }}>Recheck storage</button></div>
      </> : <>
        <p>Draft may have been saved. Check your events before creating another.</p>
        <div className="form-actions"><a className="button button-solid" href={listLink}>Check your events</a><button className="button" onClick={acknowledge}>I checked; start another draft</button></div>
      </>}
    </div>}
    <form className="organizer-form" onSubmit={event => void submit(event)}>
      <OrganizerEventFields fields={fields} update={update} disabled={pending || blocked} />
      <p className="organizer-note">This saves a private draft. Review its details before publishing; prices and the layout become fixed after the first reservation.</p>
      {error && <p className="error" role="alert">{error}</p>}
      <div className="form-actions"><button className="button button-solid" disabled={pending || blocked}>{pending ? 'Saving draft…' : 'Save draft'}</button><a className="button" href={listLink}>Back to events</a></div>
    </form>
  </>;
}
