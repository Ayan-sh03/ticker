/* Hallmark · component: organizer bookings and holds · genre: modern-minimal · theme: existing Coral tokens */
import { useEffect, useRef, useState, type RefObject } from 'react';
import { ApiError, price, request, type OrganizerEvent } from './api';
import { ErrorMessage } from './Feedback';
import { usePrivateResource } from './usePrivateResource';

type Customer = { display_name: string; email: string };
type Booking = { id: string; status: 'CONFIRMED' | 'CANCELLED'; created_at: string; cancelled_at: string | null; customer: Customer; seat_labels: string[]; price_minor: number; total_minor: number; currency: string };
type Hold = { id: string; status: 'HELD' | 'EXPIRED' | 'CANCELLED'; created_at: string; expires_at: string; ended_at: string | null; customer: Customer; seat_labels: string[] };
type Summary = { bookings: number; cancelled_bookings: number; seats_sold: number; open_holds: number; expired_holds: number; cancelled_holds: number };
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
  // The booking awaiting a second click to cancel, and the one being cancelled.
  const [confirming, setConfirming] = useState('');
  const [cancelling, setCancelling] = useState('');
  const [message, setMessage] = useState('');
  const [failure, setFailure] = useState('');
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => { if (event.data) heading.current?.focus(); }, [event.data, heading]);
  useEffect(() => { setConfirming(''); setMessage(''); setFailure(''); }, [view, pages]);
  const denied = event.forbidden || list.forbidden;
  const summary = denied ? undefined : list.data?.summary;
  // Pick by view: for one render after a switch, data may still hold the other list.
  const rows = denied ? undefined : view === 'bookings' ? list.data?.bookings : list.data?.holds;
  const error = denied ? 'You no longer have access to this organizer workspace.' : event.error ?? list.error;
  const current = denied ? undefined : event.data?.event;
  const started = current ? Date.parse(current.starts_at) <= Date.now() : true;

  // Cancelling is idempotent, so retrying after an unknown outcome is safe.
  async function cancelBooking(booking: Booking) {
    if (cancelling) return;
    setCancelling(booking.id); setMessage(''); setFailure('');
    try {
      const result = await request<{ booking: Booking }>(`${path}/bookings/${booking.id}/cancel`, { method: 'POST', headers: { 'X-Ticker-User': userID } });
      if (!mounted.current) return;
      // Adjust the totals loaded with this page, which counted the booking as active.
      const data = list.data;
      if (data?.bookings) list.replace({ ...data, bookings: data.bookings.map(row => row.id === booking.id ? result.booking : row), summary: {
        ...data.summary, bookings: data.summary.bookings - 1, cancelled_bookings: data.summary.cancelled_bookings + 1,
        seats_sold: data.summary.seats_sold - booking.seat_labels.length } });
      setMessage(`Cancelled ${booking.customer.display_name}'s booking. ${seats(booking.seat_labels)} back on sale.`);
    } catch (error) {
      if (!mounted.current) return;
      if (error instanceof ApiError && error.status === 401) { list.deny(); onSessionExpired(); }
      else if (error instanceof ApiError && error.status === 403) list.deny();
      else if (error instanceof ApiError && error.status < 500) setFailure(error.message);
      else setFailure('The cancellation may not have been saved. Try again.');
    } finally {
      if (mounted.current) { setCancelling(''); setConfirming(''); }
    }
  }

  return <>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Bookings</h1><p>{current ? `${current.title} · Who booked, and holds that did not become bookings.` : 'Review bookings and holds.'}</p></div>
    {error && <ErrorMessage message={error} retry={denied ? undefined : () => { event.retry(); list.retry(); }} />}
    {!denied && <>
      {summary && <dl className="organizer-details organizer-sales-summary">
        <div><dt>Bookings</dt><dd>{summary.bookings}</dd></div>
        <div><dt>Seats sold</dt><dd>{summary.seats_sold}</dd></div>
        <div><dt>Cancelled bookings</dt><dd>{summary.cancelled_bookings}</dd></div>
        <div><dt>Open holds</dt><dd>{summary.open_holds}</dd></div>
        <div><dt>Expired holds</dt><dd>{summary.expired_holds}</dd></div>
        <div><dt>Cancelled holds</dt><dd>{summary.cancelled_holds}</dd></div>
      </dl>}
      <div className="organizer-tabs" role="group" aria-label="Show">
        {views.map(item => <button key={item.value} className="button" aria-pressed={view === item.value}
          onClick={() => { setView(item.value); setPages([]); if (view === item.value) list.retry(); }}>{item.label}</button>)}
        <button className="button" disabled={!list.data && !list.error} onClick={() => list.retry()}>Refresh</button>
      </div>
      {message && <p role="status">{message}</p>}
      {failure && <p className="error" role="alert">{failure}</p>}
      {!rows && !error && <p className="loading" role="status">Loading…</p>}
      {rows && rows.length === 0 && <p className="organizer-empty">{pages.length ? 'No more entries.' : view === 'bookings' ? 'No bookings yet.' : `No ${views.find(item => item.value === view)!.label.toLowerCase()} yet.`}</p>}
      {rows && rows.length > 0 && <ul className="organizer-sales">{view === 'bookings'
        ? (rows as Booking[]).map(row => <li key={row.id} data-status={row.status}>
          <div><p className="organizer-sales-name">{row.customer.display_name}</p><p>{row.customer.email}</p></div>
          <div><p>{seats(row.seat_labels)}</p><p>Booked {when(row.created_at)}{row.cancelled_at && ` · Cancelled ${when(row.cancelled_at)}`}</p></div>
          <div className="organizer-sales-amount"><p className="price">{price({ price_minor: row.total_minor, currency: row.currency })}</p>
            {row.status === 'CANCELLED' ? <p>Cancelled</p> : !started && (confirming === row.id
              ? <p className="organizer-sales-confirm">Seats go back on sale.{' '}
                <button className="text-button" disabled={!!cancelling} onClick={() => void cancelBooking(row)}>{cancelling === row.id ? 'Cancelling…' : 'Cancel booking'}</button>{' '}
                <button className="text-button" disabled={!!cancelling} onClick={() => setConfirming('')}>Keep</button></p>
              : <button className="text-button" disabled={!!cancelling} onClick={() => { setConfirming(row.id); setMessage(''); setFailure(''); }}>Cancel…</button>)}
          </div>
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
