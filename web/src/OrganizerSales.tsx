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
  const [offset, setOffset] = useState(0);
  const event = usePrivateResource<{ event: OrganizerEvent }>(path, userID, onSessionExpired);
  const bookings = usePrivateResource<{ summary: Summary; bookings: Booking[] }>(`${path}/bookings?limit=${pageSize}&offset=${view === 'bookings' ? offset : 0}`, userID, onSessionExpired);
  const holds = usePrivateResource<{ holds: Hold[] }>(view === 'bookings' ? null : `${path}/holds?status=${view}&limit=${pageSize}&offset=${offset}`, userID, onSessionExpired);
  useEffect(() => { if (event.data) heading.current?.focus(); }, [event.data, heading]);
  const summary = bookings.data?.summary;
  const rows = view === 'bookings' ? bookings.data?.bookings : holds.data?.holds;
  const error = event.error ?? bookings.error ?? (view === 'bookings' ? undefined : holds.error);
  const current = event.data?.event;

  return <>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Bookings</h1><p>{current ? `${current.title} · Who booked, and holds that did not become bookings.` : 'Review bookings and holds.'}</p></div>
    {error && <ErrorMessage message={error} retry={() => { event.retry(); bookings.retry(); holds.retry(); }} />}
    {summary && <dl className="organizer-details organizer-sales-summary">
      <div><dt>Bookings</dt><dd>{summary.bookings}</dd></div>
      <div><dt>Seats sold</dt><dd>{summary.seats_sold}</dd></div>
      <div><dt>Open holds</dt><dd>{summary.open_holds}</dd></div>
      <div><dt>Expired holds</dt><dd>{summary.expired_holds}</dd></div>
      <div><dt>Cancelled holds</dt><dd>{summary.cancelled_holds}</dd></div>
    </dl>}
    <div className="organizer-tabs" role="group" aria-label="Show">
      {views.map(item => <button key={item.value} className="button" aria-pressed={view === item.value}
        onClick={() => { setView(item.value); setOffset(0); }}>{item.label}</button>)}
    </div>
    {!rows && !error && <p className="loading" role="status">Loading…</p>}
    {rows && rows.length === 0 && <p className="organizer-empty">{offset ? 'No more entries on this page.' : view === 'bookings' ? 'No bookings yet.' : `No ${views.find(item => item.value === view)!.label.toLowerCase()} yet.`}</p>}
    {rows && rows.length > 0 && <ul className="organizer-sales">{view === 'bookings'
      ? (rows as Booking[]).map(row => <li key={row.id}>
        <div><p className="organizer-sales-name">{row.customer.display_name}</p><p>{row.customer.email}</p></div>
        <div><p>{seats(row.seat_labels)}</p><p>Booked {when(row.created_at)}</p></div>
        <p className="price">{price({ price_minor: row.total_minor, currency: row.currency })}</p>
      </li>)
      : (rows as Hold[]).map(row => <li key={row.id}>
        <div><p className="organizer-sales-name">{row.customer.display_name}</p><p>{row.customer.email}</p></div>
        <div><p>{seats(row.seat_labels)}</p><p>Held {when(row.created_at)}</p></div>
        <p>{row.status === 'HELD' ? `Expires ${when(row.expires_at)}` : `${row.status === 'EXPIRED' ? 'Expired' : 'Cancelled'} ${when(row.ended_at ?? row.expires_at)}`}</p>
      </li>)}</ul>}
    {(offset > 0 || rows?.length === pageSize) && <nav className="pagination" aria-label="Pages">
      <button className="button" disabled={offset === 0} onClick={() => setOffset(value => value - pageSize)}>Previous</button>
      <button className="button" disabled={!rows || rows.length < pageSize || offset >= 10000} onClick={() => setOffset(value => value + pageSize)}>Next</button>
    </nav>}
  </>;
}
