/* Hallmark · component: organizer bookings and holds · genre: modern-minimal · theme: existing Coral tokens */
import { useEffect, useState, type RefObject } from 'react';
import { price, type OrganizerEvent } from './api';
import { ErrorMessage } from './Feedback';
import { usePrivateResource } from './usePrivateResource';

type Customer = { display_name: string; email: string };
type Booking = { id: string; created_at: string; customer: Customer; seat_labels: string[]; price_minor: number; total_minor: number; currency: string };
type Hold = { id: string; status: 'HELD' | 'EXPIRED' | 'CANCELLED'; created_at: string; expires_at: string; ended_at: string | null; customer: Customer; seat_labels: string[] };
type Summary = { bookings: number; seats_sold: number; open_holds: number; expired_holds: number; cancelled_holds: number };
type View = 'bookings' | 'open' | 'expired' | 'cancelled';

const pageSize = 20;
const views: { value: View; label: string }[] = [
  { value: 'bookings', label: 'Bookings' }, { value: 'open', label: 'Open holds' },
  { value: 'expired', label: 'Expired' }, { value: 'cancelled', label: 'Cancelled' },
];
const when = (value: string) => new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
const seats = (labels: string[]) => `${labels.length} ${labels.length === 1 ? 'seat' : 'seats'} · ${labels.join(', ')}`;

export function OrganizerSales({ tenantID, eventID, userID, heading, onSessionExpired }: {
  tenantID: string; eventID: string; userID: string; heading: RefObject<HTMLHeadingElement | null>; onSessionExpired: () => void;
}) {
  const path = `/organizer/tenants/${tenantID}/events/${eventID}`;
  const [view, setView] = useState<View>('bookings');
  // Each entry is the "after" cursor of a page past the first, so Previous can step back.
  const [pages, setPages] = useState<string[]>([]);
  const after = pages.length ? `&after=${encodeURIComponent(pages[pages.length - 1])}` : '';
  const event = usePrivateResource<{ event: OrganizerEvent }>(path, userID, onSessionExpired);
  // Totals come with every page, so they always match the rows shown.
  const list = usePrivateResource<{ summary: Summary; bookings?: Booking[]; holds?: Hold[]; next: string | null }>(view === 'bookings'
    ? `${path}/bookings?limit=${pageSize}${after}` : `${path}/holds?status=${view}&limit=${pageSize}${after}`, userID, onSessionExpired);
  useEffect(() => { if (event.data) heading.current?.focus(); }, [event.data, heading]);
  const denied = event.forbidden || list.forbidden;
  const summary = denied ? undefined : list.data?.summary;
  // Pick by view: for one render after a switch, data may still hold the other list.
  const rows = denied ? undefined : view === 'bookings' ? list.data?.bookings : list.data?.holds;
  const error = denied ? 'You no longer have access to this organizer workspace.' : event.error ?? list.error;
  const current = denied ? undefined : event.data?.event;

  return <>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Bookings</h1><p>{current ? `${current.title} · Who booked, and holds that did not become bookings.` : 'Review bookings and holds.'}</p></div>
    {error && <ErrorMessage message={error} retry={denied ? undefined : () => { event.retry(); list.retry(); }} />}
    {!denied && <>
      {summary && <dl className="organizer-details organizer-sales-summary">
        <div><dt>Bookings</dt><dd>{summary.bookings}</dd></div>
        <div><dt>Seats sold</dt><dd>{summary.seats_sold}</dd></div>
        <div><dt>Open holds</dt><dd>{summary.open_holds}</dd></div>
        <div><dt>Expired holds</dt><dd>{summary.expired_holds}</dd></div>
        <div><dt>Cancelled holds</dt><dd>{summary.cancelled_holds}</dd></div>
      </dl>}
      <div className="organizer-tabs" role="group" aria-label="Show">
        {views.map(item => <button key={item.value} className="button" aria-pressed={view === item.value}
          onClick={() => { setView(item.value); setPages([]); if (view === item.value) list.retry(); }}>{item.label}</button>)}
        <button className="button" disabled={!list.data && !list.error} onClick={() => list.retry()}>Refresh</button>
      </div>
      {!rows && !error && <p className="loading" role="status">Loading…</p>}
      {rows && rows.length === 0 && <p className="organizer-empty">{pages.length ? 'No more entries.' : view === 'bookings' ? 'No bookings yet.' : `No ${views.find(item => item.value === view)!.label.toLowerCase()} yet.`}</p>}
      {rows && rows.length > 0 && <ul className="organizer-sales">{view === 'bookings'
        ? (rows as Booking[]).map(row => <li key={row.id}>
          <div><p className="organizer-sales-name">{row.customer.display_name}</p><p>{row.customer.email}</p></div>
          <div><p>{seats(row.seat_labels)}</p><p>Booked {when(row.created_at)}</p></div>
          <p className="price">{price({ price_minor: row.total_minor, currency: row.currency })}</p>
        </li>)
        : (rows as Hold[]).map(row => <li key={row.id}>
          <div><p className="organizer-sales-name">{row.customer.display_name}</p><p>{row.customer.email}</p></div>
          <div><p>{seats(row.seat_labels)}</p><p>Held {when(row.created_at)}</p></div>
          {/* Holds from before the audit trail have no end time; an expired one ended at its deadline. */}
          <p>{row.status === 'HELD' ? `Expires ${when(row.expires_at)}` : row.status === 'EXPIRED' ? `Expired ${when(row.ended_at ?? row.expires_at)}`
            : row.ended_at ? `Cancelled ${when(row.ended_at)}` : 'Cancelled · time unavailable'}</p>
        </li>)}</ul>}
      {(pages.length > 0 || list.data?.next) && <nav className="pagination" aria-label="Pages">
        <button className="button" disabled={!pages.length} onClick={() => setPages(value => value.slice(0, -1))}>Previous</button>
        <button className="button" disabled={!list.data?.next} onClick={() => { const next = list.data?.next; if (next) setPages(value => [...value, next]); }}>Next</button>
      </nav>}
    </>}
  </>;
}
