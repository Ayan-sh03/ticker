import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ApiError, request, type OrganizerEvent, type UpdateEventInput } from './api';
import { OrganizerEventFields, draftPayload, fieldsForEvent, type Fields } from './OrganizerEventFields';

export function OrganizerEditForm({ event, userID, onSaved, onCancel, onRecheck, onForbidden, onSessionExpired }: {
  event: OrganizerEvent; userID: string; onSaved: (event: OrganizerEvent) => void;
  onCancel: () => void; onRecheck: () => void; onForbidden: () => void; onSessionExpired: () => void;
}) {
  const [fields, setFields] = useState(() => fieldsForEvent(event));
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [needsRecheck, setNeedsRecheck] = useState(false);
  const busy = useRef(false);
  const mounted = useRef(true);
  const submission = useRef<AbortController | null>(null);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; submission.current?.abort(); }; }, []);

  function update(field: keyof Fields, value: string) { setFields(previous => ({ ...previous, [field]: value })); setError(''); }
  async function submit(formEvent: FormEvent<HTMLFormElement>) {
    formEvent.preventDefault();
    if (busy.current || needsRecheck) return;
    let payload: UpdateEventInput;
    try { payload = { ...draftPayload(fields, event), expected_version: event.version }; }
    catch (failure) { setError((failure as Error).message); return; }
    busy.current = true;
    const controller = new AbortController();
    submission.current = controller;
    setPending(true); setError('');
    try {
      const result = await request<{ event: OrganizerEvent }>(`/organizer/tenants/${event.tenant_id}/events/${event.id}`, {
        method: 'PUT', signal: controller.signal, headers: { 'X-Ticker-User': userID }, body: JSON.stringify(payload),
      });
      if (result?.event?.id !== event.id || result.event.tenant_id !== event.tenant_id
        || !Number.isSafeInteger(result.event.version) || result.event.version <= event.version
        || typeof result.event.price_layout_editable !== 'boolean') throw new Error('Unconfirmed update');
      if (mounted.current) onSaved(result.event);
    } catch (failure) {
      if (!mounted.current) return;
      if (failure instanceof ApiError && failure.status === 401) { onForbidden(); onSessionExpired(); }
      else if (failure instanceof ApiError && failure.status === 403) onForbidden();
      else if (failure instanceof ApiError && failure.status === 409) {
        setNeedsRecheck(true);
        setError(`${failure.message} Recheck the event before editing again.`);
      } else if (failure instanceof ApiError && failure.status >= 400 && failure.status < 500) {
        setError(failure.message);
        if (failure.status === 404) setNeedsRecheck(true);
      } else {
        setNeedsRecheck(true);
        setError('Your changes may have been saved. Recheck the event before editing again.');
      }
    } finally { busy.current = false; if (mounted.current) setPending(false); }
  }

  return <form className="organizer-form" onSubmit={formEvent => void submit(formEvent)}>
    {!event.price_layout_editable && <p className="organizer-note">Price, currency, and the seat layout are fixed after the first reservation. You can still edit event details and dates.</p>}
    <OrganizerEventFields fields={fields} update={update} disabled={pending || needsRecheck} priceLayoutEditable={event.price_layout_editable} originalCurrency={event.currency} mode="edit" />
    <p className="organizer-note">{event.status === 'PUBLISHED' ? 'Saved changes appear on the customer page.' : 'Saved changes stay private until you publish.'}</p>
    {error && <p className="error" role="alert">{error}</p>}
    <div className="form-actions">
      {needsRecheck ? <button className="button button-solid" type="button" onClick={onRecheck}>Recheck event</button>
        : <button className="button button-solid" disabled={pending}>{pending ? 'Saving changes…' : 'Save changes'}</button>}
      <button className="button" type="button" disabled={pending} onClick={needsRecheck ? onRecheck : onCancel}>Cancel</button>
    </div>
  </form>;
}
