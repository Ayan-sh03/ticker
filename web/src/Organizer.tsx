import { useEffect, useRef, useState, type RefObject } from 'react';
import { ApiError, date, price, request, zoneName, type OrganizerEvent, type Session, type Tenant } from './api';
import { usePrivateResource } from './usePrivateResource';
import { OrganizerSeats } from './OrganizerSeats';
import { OrganizerSales } from './OrganizerSales';
import { EventInsights, TenantInsights, money, type EventSales } from './OrganizerInsights';
import { ErrorMessage } from './Feedback';
import { OrganizerForm } from './OrganizerForm';
import { OrganizerEditForm } from './OrganizerEditForm';
import { OrganizerSelect } from './OrganizerSelect';

type Props = {
  route: string;
  session: Session | null;
  checking: boolean;
  heading: RefObject<HTMLHeadingElement | null>;
  onLogin: () => void;
  onSessionExpired: () => void;
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function Organizer({ route, session, checking, heading, onLogin, onSessionExpired }: Props) {
  useEffect(() => { heading.current?.focus(); }, [heading]);
  if (checking) return <><div className="page-heading"><h1 ref={heading} tabIndex={-1}>Organizer</h1></div><p className="loading" role="status">Checking your account…</p></>;
  if (!session) return <>
    <a className="back-link" href="#">← All events</a>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Organizer</h1><p>Log in to manage your events.</p></div>
    <button className="button button-solid" onClick={onLogin}>Log in</button>
  </>;
  return <Workspace route={route} session={session} heading={heading} onSessionExpired={onSessionExpired} />;
}

function Workspace({ route, session, heading, onSessionExpired }: Omit<Props, 'checking' | 'onLogin'> & { session: Session }) {
  const { data, error, retry } = usePrivateResource<{ tenants: Tenant[] }>('/organizer/tenants', session.user.id, onSessionExpired);
  const [, tenantID, target, extra] = route.split('/');
  useEffect(() => { if (data) heading.current?.focus(); }, [data, heading]);
  if (!data) return <>
    <a className="back-link" href="#">← All events</a>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Organizer</h1><p>Manage your events.</p></div>
    {error ? <ErrorMessage message={error} retry={retry} /> : <p className="loading" role="status">Loading your workspaces…</p>}
  </>;
  if (data.tenants.length === 0) return <>
    <a className="back-link" href="#">← All events</a>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Organizer</h1><p>Your account has no organizer memberships.</p></div>
    <p className="organizer-empty">You can still browse events and book a seat.</p>
  </>;
  const tenant = tenantID ? data.tenants.find(item => item.id === tenantID) : data.tenants[0];
  const view = extra === 'seats' || extra === 'bookings' || extra === 'insights' ? extra : undefined;
  if (!tenant || (extra && !view) || (target && target !== 'new' && target !== 'insights' && !uuid.test(target)) || (view && !uuid.test(target))) return <>
    <a className="back-link" href="#organizer">← Organizer</a>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Workspace unavailable</h1><p>This workspace or event is unavailable to your account.</p></div>
  </>;
  const base = `organizer/${tenant.id}`;
  return <>
    <a className="back-link" href={target ? `#${base}` : '#'}>← {target ? 'Your events' : 'All events'}</a>
    {/* A single workspace is a fact, not a choice; its name is in each page's subline. */}
    {data.tenants.length > 1 && !target && <div className="organizer-toolbar"><div className="organizer-field"><label htmlFor="organizer-tenant">Workspace</label>
      <OrganizerSelect id="organizer-tenant" label="Workspace" value={tenant.id}
        options={data.tenants.map(item => ({ value: item.id, label: item.name }))}
        onChange={value => { location.hash = `organizer/${value}`; }} />
    </div></div>}
    {view && <EventTabs base={base} eventID={target} current={view} />}
    {target === 'new' ? <>
      <div className="page-heading"><h1 ref={heading} tabIndex={-1}>New event</h1><p>Create a private draft for {tenant.name}.</p></div>
      <OrganizerForm userID={session.user.id} tenantID={tenant.id} onSessionExpired={onSessionExpired} />
    </> : target === 'insights' ? <TenantInsights tenantID={tenant.id} tenantName={tenant.name} userID={session.user.id} heading={heading} onSessionExpired={onSessionExpired} />
      : view === 'insights' ? <EventInsights tenantID={tenant.id} eventID={target} userID={session.user.id} heading={heading} onSessionExpired={onSessionExpired} />
      : view === 'seats' ? <OrganizerSeats tenantID={tenant.id} eventID={target} userID={session.user.id} heading={heading} onSessionExpired={onSessionExpired} />
      : view === 'bookings' ? <OrganizerSales tenantID={tenant.id} eventID={target} userID={session.user.id} heading={heading} onSessionExpired={onSessionExpired} />
      : target ? <EventDetail tenantID={tenant.id} eventID={target} userID={session.user.id} heading={heading} onSessionExpired={onSessionExpired} />
      : <EventList tenant={tenant} userID={session.user.id} heading={heading} onSessionExpired={onSessionExpired} />}
  </>;
}

type EventView = 'overview' | 'seats' | 'bookings' | 'insights';
const eventViews: [EventView, string, string][] = [['overview', 'Overview', ''], ['seats', 'Seats', '/seats'], ['bookings', 'Bookings', '/bookings'], ['insights', 'Insights', '/insights']];

// One row of tabs ties a published event's pages together.
function EventTabs({ base, eventID, current }: { base: string; eventID: string; current: EventView }) {
  return <nav className="event-tabs" aria-label="Event pages">
    <div className="segmented">{eventViews.map(([key, label, path]) =>
      <a key={key} className="button" href={`#${base}/${eventID}${path}`} aria-current={current === key ? 'page' : undefined}>{label}</a>)}</div>
    <a className="text-button" href={`#${eventID}`}>View customer page</a>
  </nav>;
}

function EventList({ tenant, userID, heading, onSessionExpired }: {
  tenant: Tenant; userID: string; heading: Props['heading']; onSessionExpired: () => void;
}) {
  const [offset, setOffset] = useState(0);
  const base = `organizer/${tenant.id}`;
  const { data, error, retry } = usePrivateResource<{ events: OrganizerEvent[] }>(`/organizer/tenants/${tenant.id}/events?limit=20&offset=${offset}`, userID, onSessionExpired);
  // Sales for the latest published events; a row without them still links to its pages.
  const insights = usePrivateResource<{ events: EventSales[] }>(`/organizer/tenants/${tenant.id}/insights`, userID, onSessionExpired);
  const sales = new Map((insights.data?.events ?? []).map(item => [item.id, item]));
  return <>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Your events</h1><p>{tenant.name} · Drafts stay private until you publish.</p></div>
    <div className="form-actions organizer-list-actions"><a className="button button-solid" href={`#${base}/new`}>New event</a>
      <a className="button" href={`#${base}/insights`}>Workspace insights</a></div>
    {error && <ErrorMessage message={error} retry={retry} />}
    {!data && !error && <p className="loading" role="status">Loading events…</p>}
    {data && <>
      {data.events.length === 0 && <p className="organizer-empty">{offset ? 'No more events on this page.' : 'No events yet. Create your first draft.'}</p>}
      <ul className="organizer-events">{data.events.map(event => {
        const sold = sales.get(event.id);
        const draft = event.status === 'DRAFT';
        return <li key={event.id} data-draft={draft || undefined}>
          <div><h2><a href={`#${base}/${event.id}`}>{event.title}</a></h2>
            <p>{draft && <><span className="organizer-status draft">Draft</span> · </>}{date(event)} · {event.venue_name} · {price(event)} per seat</p></div>
          {draft ? <p className="organizer-event-sold">Private until you publish</p> : <>
            <div className="organizer-event-sold">{sold ? <><span>{sold.seats_sold} of {sold.capacity} sold</span>
              <span className="insight-track" aria-hidden="true"><span style={{ width: `${sold.capacity ? Math.min(100, sold.seats_sold / sold.capacity * 100) : 0}%` }} /></span></>
              : <span>{insights.error ? 'Sales unavailable' : insights.data ? 'No sales data' : 'Loading sales…'}</span>}</div>
            <p className="price">{sold?.bookings ? money(sold.net_minor, sold.currency) : '—'}<small>net</small></p></>}
          <nav className="organizer-event-links" aria-label={`${event.title} pages`}>{draft ? <a href={`#${base}/${event.id}`}>Review draft</a>
            : eventViews.slice(1).map(([key, label, path]) => <a key={key} href={`#${base}/${event.id}${path}`}>{label}</a>)}</nav>
        </li>;
      })}</ul>
    </>}
    {(offset > 0 || data?.events.length === 20) && <nav className="pagination" aria-label="Organizer event pages">
      <button className="button" disabled={offset === 0 || !data} onClick={() => setOffset(value => value - 20)}>Previous</button>
      <button className="button" disabled={!data || data.events.length < 20 || offset >= 10000} onClick={() => setOffset(value => value + 20)}>Next</button>
    </nav>}
  </>;
}

function EventDetail({ tenantID, eventID, userID, heading, onSessionExpired }: {
  tenantID: string; eventID: string; userID: string; heading: Props['heading']; onSessionExpired: () => void;
}) {
  const path = `/organizer/tenants/${tenantID}/events/${eventID}`;
  const resource = usePrivateResource<{ event: OrganizerEvent }>(path, userID, onSessionExpired);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [uncertain, setUncertain] = useState(false);
  const [editing, setEditing] = useState(false);
  const [saved, setSaved] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteError, setDeleteError] = useState('');
  const [deleteNeedsRecheck, setDeleteNeedsRecheck] = useState(false);
  const deleteHeading = useRef<HTMLHeadingElement | null>(null);
  const submission = useRef<AbortController | null>(null);
  const busy = useRef(false);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; submission.current?.abort(); }; }, []);
  useEffect(() => { heading.current?.focus(); }, [editing, heading]);
  useEffect(() => { if (confirmDelete) deleteHeading.current?.focus(); else heading.current?.focus(); }, [confirmDelete, heading]);
  function recheck() {
    setEditing(false); setSaved(false); setError(''); setUncertain(false);
    setConfirmDelete(false); setDeleteError(''); setDeleteNeedsRecheck(false);
    resource.retry();
  }
  async function remove() {
    const current = resource.data?.event;
    if (busy.current || !current || !current.price_layout_editable || deleteNeedsRecheck) return;
    busy.current = true;
    const controller = new AbortController();
    submission.current = controller;
    setPending(true); setDeleteError('');
    try {
      const result = await request<void>(path, { method: 'DELETE', signal: controller.signal,
        headers: { 'X-Ticker-User': userID }, body: JSON.stringify({ expected_version: current.version }) });
      if (result !== undefined) throw new Error('Unconfirmed deletion');
      if (mounted.current) location.hash = `organizer/${tenantID}`;
    } catch (failure) {
      if (!mounted.current) return;
      if (failure instanceof ApiError && failure.status === 401) { resource.deny(); onSessionExpired(); }
      else if (failure instanceof ApiError && failure.status === 403) resource.deny();
      else if (failure instanceof ApiError && failure.status === 404) location.hash = `organizer/${tenantID}`;
      else {
        setDeleteNeedsRecheck(true);
        setDeleteError(failure instanceof ApiError && failure.status < 500
          ? `${failure.message} Recheck the event before deleting again.`
          : 'Deletion may have completed. Recheck the event before trying again.');
      }
    } finally { busy.current = false; if (mounted.current) setPending(false); }
  }
  async function publish() {
    if (busy.current || deleteNeedsRecheck || resource.data?.event.status !== 'DRAFT') return;
    busy.current = true;
    const controller = new AbortController();
    submission.current = controller;
    setPending(true); setError(''); setUncertain(false);
    try {
      const result = await request<{ event: OrganizerEvent }>(`${path}/publish`, { method: 'POST', signal: controller.signal, headers: { 'X-Ticker-User': userID } });
      if (result?.event?.id !== eventID || result.event.status !== 'PUBLISHED') throw new Error('Unconfirmed publication');
      if (mounted.current) resource.replace(result);
    } catch (failure) {
      if (!mounted.current) return;
      if (failure instanceof ApiError && failure.status === 401) { resource.deny(); onSessionExpired(); }
      else if (failure instanceof ApiError && failure.status === 403) resource.deny();
      else {
        const unknown = !(failure instanceof ApiError && failure.status < 500);
        setUncertain(unknown);
        setError(unknown ? 'Publication may have completed. Recheck this event, or retry publication safely.' : failure.message);
      }
    } finally { busy.current = false; if (mounted.current) setPending(false); }
  }
  const event = resource.data?.event;
  const localZone = zoneName();
  const formatTime = (value: string) => new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
  return <>
    {event?.status === 'PUBLISHED' && !editing && <EventTabs base={`organizer/${tenantID}`} eventID={event.id} current="overview" />}
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>{editing ? 'Edit event' : event?.title ?? 'Event review'}</h1><p>{event ? editing ? `Update ${event.title}.` : event.status === 'DRAFT' ? 'Private draft · Review the details before publishing.' : 'Published · Your event is visible to customers.' : 'Review your event details.'}</p></div>
    {resource.error && <ErrorMessage message={resource.error} retry={resource.retry} />}
    {!event && !resource.error && <p className="loading" role="status">Loading event…</p>}
    {event && (editing ? <OrganizerEditForm key={`${event.id}:${event.version}`} event={event} userID={userID}
      onSaved={updated => { resource.replace({ event: updated }); setEditing(false); setSaved(true); }}
      onCancel={() => setEditing(false)} onRecheck={recheck} onForbidden={resource.deny} onSessionExpired={onSessionExpired} /> : <>
      {saved && <p role="status">Changes saved.</p>}
      <dl className="organizer-details">
        <div><dt>Venue</dt><dd>{event.venue_name}</dd></div>
        <div><dt>Price per seat</dt><dd>{price(event)} <span className="organizer-note">{event.currency}</span></dd></div>
        <div><dt>Event starts</dt><dd>{formatTime(event.starts_at)}</dd></div>
        <div><dt>Sales open</dt><dd>{formatTime(event.sales_open_at)}</dd></div>
        <div><dt>Seat layout</dt><dd>{event.layout_rows} rows × {event.layout_columns} seats · {event.layout_rows * event.layout_columns} total</dd></div>
        <div><dt>Status</dt><dd><span className={`organizer-status ${event.status.toLowerCase()}`}>{event.status === 'DRAFT' ? 'Draft' : 'Published'}</span></dd></div>
      </dl>
      <p className="organizer-note organizer-zone">Times shown in {localZone}.</p>
      {event.description && <div className="organizer-description"><h2>Description</h2><p>{event.description}</p></div>}
      {event.status === 'DRAFT' && <div className="organizer-confirm"><h2>Ready to publish?</h2>
        <p>Publishing makes this event public and creates its seats. You can edit price, currency, and the seat layout until the first reservation.</p>
        {error && <p className="error" role="alert">{error}</p>}
        <div className="form-actions"><button className="button button-solid" disabled={pending || confirmDelete || deleteNeedsRecheck} onClick={() => void publish()}>{pending ? confirmDelete ? 'Deleting…' : 'Publishing…' : uncertain ? 'Retry publication' : 'Publish event'}</button>
          {uncertain && <button className="button" disabled={pending} onClick={recheck}>Recheck event</button>}</div>
      </div>}
      <section className="organizer-confirm" aria-labelledby="organizer-manage-title"><h2 id="organizer-manage-title">{event.price_layout_editable ? 'Edit or delete' : 'Edit'}</h2>
      {event.status === 'PUBLISHED' && <p className="organizer-note">{event.price_layout_editable ? 'You can edit price, currency, and the seat layout until the first reservation.' : 'Price, currency, and the seat layout are fixed after the first reservation.'}</p>}
      <div className="form-actions">
        <button className="button" disabled={pending || confirmDelete || deleteNeedsRecheck} onClick={() => { setSaved(false); setEditing(true); }}>Edit event</button>
        {event.price_layout_editable && <button className="button" disabled={pending || confirmDelete || deleteNeedsRecheck} onClick={() => { setSaved(false); setDeleteError(''); setConfirmDelete(true); }}>Delete event</button>}
      </div>
      {!event.price_layout_editable && <p className="organizer-note">The event cannot be deleted after its first reservation, so its booking history stays intact.</p>}
      {confirmDelete && <div className="organizer-confirm" role="region" aria-labelledby="organizer-delete-title">
        <h2 id="organizer-delete-title" ref={deleteHeading} tabIndex={-1}>Delete {event.title}?</h2>
        <p>This permanently removes the event and its seats.</p>
        {deleteError && <p className="error" role="alert">{deleteError}</p>}
        <div className="form-actions">
          {deleteNeedsRecheck ? <button className="button" onClick={recheck}>Recheck event</button>
            : <button className="button button-danger" disabled={pending} onClick={() => void remove()}>{pending ? 'Deleting…' : 'Delete event'}</button>}
          <button className="button" disabled={pending} onClick={() => { if (deleteNeedsRecheck) recheck(); else setConfirmDelete(false); }}>Cancel</button>
        </div>
      </div>}
      </section>
    </>)}
  </>;
}
