import { memo, useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { ApiError, date, price, request, useResource, type Event, type Seat, type Session } from './api';
import { ErrorMessage } from './Feedback';
import { maxSeats, seatConflict, useReservation } from './useReservation';
import { groupSeats } from './seatGroups';

const emptySeats: Seat[] = [];
const emptyIDs: string[] = [];

const statuses = { AVAILABLE: 'Available', HELD: 'Held', SOLD: 'Booked', BLOCKED: 'Blocked' };
// Percentage of bookable seats remaining; blocked seats do not add capacity.
const availabilityThresholds = { fewPercent: 10, limitedPercent: 25 };

const seatCount = (n: number) => `${n} ${n === 1 ? 'seat' : 'seats'}`;

const SeatAvailability = memo(function SeatAvailability({ seats, stale }: { seats: Seat[]; stale: boolean }) {
  const counts = { AVAILABLE: 0, HELD: 0, SOLD: 0, BLOCKED: 0 };
  for (const seat of seats) counts[seat.status]++;
  if (!seats.length) return <p className="availability-empty" role="status">{stale ? 'No seats were listed at the last refresh.' : 'No seats are listed for this event.'}</p>;
  const capacity = counts.AVAILABLE + counts.HELD + counts.SOLD;
  const remainingPercent = capacity ? counts.AVAILABLE / capacity * 100 : 0;
  let label = 'Seats available';
  let level = 'good';
  if (stale) { label = 'Last known availability'; level = 'neutral'; }
  else if (!counts.AVAILABLE) {
    label = counts.HELD ? 'Temporarily unavailable' : capacity && counts.SOLD === capacity ? 'Sold out' : 'No seats available';
    level = label === 'Sold out' ? 'low' : 'neutral';
  } else if (remainingPercent <= availabilityThresholds.fewPercent) { label = 'Few seats left'; level = 'low'; }
  else if (remainingPercent <= availabilityThresholds.limitedPercent) { label = 'Limited availability'; level = 'limited'; }
  const detail = [
    { count: counts.HELD, label: 'held' },
    { count: counts.SOLD, label: 'booked' },
    { count: counts.BLOCKED, label: 'blocked' },
  ].filter(item => item.count).map(item => `${item.count} ${item.label}`).join(' · ');
  return <div className="availability-summary" role="status" aria-atomic="true">
    <div className="availability-heading"><p className="remaining-count"><strong>{counts.AVAILABLE}</strong> {counts.AVAILABLE === 1 ? 'seat' : 'seats'} remaining</p><span className="availability-label" data-level={level}>{label}</span></div>
    {detail && <p className="availability-detail">{detail}</p>}
  </div>;
});

function SeatSummary({ seats }: { seats: Seat[] }) {
  return <ul className="seat-summary">{seats.map(seat => <li key={seat.id} className="selected-seat">Seat {seat.label}<small>{seat.section} · Row {seat.row_number}</small></li>)}</ul>;
}

// Signed-in customers can mark interest. Requests belong to one event and
// account; a change of either aborts them, so a stale reply (even a 401)
// cannot touch the newer session.
function Interest({ eventID, userID, onSessionExpired }: { eventID: string; userID: string; onSessionExpired: () => void }) {
  const [interested, setInterested] = useState<boolean>();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<'' | 'load' | 'forbidden' | 'save'>('');
  const [attempt, setAttempt] = useState(0);
  const writes = useRef<AbortController>(null);
  useEffect(() => {
    const controller = new AbortController();
    setInterested(undefined); setFailure('');
    request<{ interested: boolean }>(`/events/${eventID}/interest`, { signal: controller.signal, headers: { 'X-Ticker-User': userID } })
      .then(result => { if (!controller.signal.aborted) setInterested(result.interested); })
      .catch(reason => {
        if (controller.signal.aborted) return;
        if (reason instanceof ApiError && reason.status === 401) onSessionExpired();
        else setFailure(reason instanceof ApiError && reason.status === 403 ? 'forbidden' : 'load');
      });
    return () => { controller.abort(); writes.current?.abort(); writes.current = null; setBusy(false); };
  }, [eventID, userID, attempt]);
  async function toggle() {
    writes.current?.abort();
    const controller = new AbortController();
    writes.current = controller;
    setBusy(true); setFailure('');
    try {
      const result = await request<{ interested: boolean }>(`/events/${eventID}/interest`, { method: interested ? 'DELETE' : 'PUT', signal: controller.signal, headers: { 'X-Ticker-User': userID } });
      if (!controller.signal.aborted) setInterested(result.interested);
    } catch (reason) {
      if (controller.signal.aborted) return;
      if (reason instanceof ApiError && reason.status === 401) onSessionExpired(); else setFailure('save');
    } finally { if (writes.current === controller) { writes.current = null; setBusy(false); } }
  }
  if (interested === undefined) return <p className="interest">{failure === 'forbidden' ? <span className="error" role="alert">Interest is not available for this account.</span>
    : failure ? <><span className="error" role="alert">Your interest could not be loaded.</span><button className="button" onClick={() => setAttempt(value => value + 1)}>Retry</button></>
    : <span className="loading" role="status">Checking interest…</span>}</p>;
  return <p className="interest"><button className="button" aria-pressed={interested} disabled={busy} onClick={() => void toggle()}>
    {interested && <span aria-hidden="true">✓</span>}Interested</button>
    {failure === 'save' && <span className="error" role="alert">Your interest could not be saved. Try again.</span>}</p>;
}

// Countdown ticks leave this subtree untouched. Inventory, selection, ownership,
// stale reads and the six-seat cap still update every button when they change.
const SeatMap = memo(function SeatMap({ groups, list, ownedIDs, selected, occupied, full, showOwnership, booked, stale, onToggle }: {
  groups: Map<string, Seat[]>; list: boolean; ownedIDs: string[]; selected: Seat[];
  occupied: boolean; full: boolean; showOwnership: boolean; booked: boolean; stale: boolean; onToggle: (id: string) => void;
}) {
  return <>
    {[...groups].map(([group, rowSeats]) => <div className="seat-group" key={group}>
      <h3>{group}</h3><ul className={list ? 'seat-list' : 'seat-grid'}>{rowSeats.map(seat => {
        const unavailable = seat.status !== 'AVAILABLE';
        const yours = ownedIDs.includes(seat.id) && showOwnership;
        const chosen = !occupied && selected.some(value => value.id === seat.id);
        const capped = !unavailable && !chosen && !occupied && full;
        return <li key={seat.id}><button className={`seat ${list ? 'seat-line' : ''} ${yours ? 'seat-owned' : ''} ${capped ? 'seat-capped' : ''}`} data-seat-status={seat.status} disabled={unavailable || stale || occupied || capped} aria-pressed={chosen || yours} aria-label={`${seat.label}, ${yours ? booked ? 'your confirmed seat' : 'your reservation' : statuses[seat.status]}${chosen ? ', selected' : ''}${capped ? `, ${maxSeats}-seat limit reached` : ''}`} onClick={() => onToggle(seat.id)}>
          <span>{seat.label}</span><span className="seat-state" aria-hidden="true">{yours ? list ? 'Yours' : '✓' : list || unavailable ? statuses[seat.status] : chosen ? '✓' : ''}</span>
        </button></li>;
      })}</ul>
    </div>)}
  </>;
});

export function EventView({ id, heading, session, checking, selectedIDs, select, onLogin, onSessionExpired }: {
  id: string; heading: RefObject<HTMLHeadingElement | null>; session: Session | null;
  checking: boolean; onLogin: () => void; onSessionExpired: () => void;
  selectedIDs: string[]; select: (value: string[]) => void;
}) {
  const event = useResource<{ event: Event }>(`/events/${encodeURIComponent(id)}`);
  const inventory = useResource<{ seats: Seat[] }>(event.data ? `/events/${encodeURIComponent(id)}/seats` : null, 2000);
  const [list, setList] = useState(false);
  // Labels of selected seats that someone else took before this customer reserved.
  const [lost, setLost] = useState<string[]>([]);
  const [confirmingCancel, setConfirmingCancel] = useState(false);
  const hold = useReservation(id, session?.user.id, onSessionExpired);
  // Older replay responses may lack a quote; the fresh hold read supplies it
  // before confirmation becomes available.
  const heldQuote = hold.reservation?.price_minor !== undefined && hold.reservation.currency
    ? { price_minor: hold.reservation.price_minor, currency: hold.reservation.currency } : undefined;
  const ownedIDs = hold.booking?.seat_ids ?? hold.reservation?.seat_ids ?? (hold.pending?.kind === 'reserve' ? hold.pending.seatIDs : emptyIDs);
  const seats = inventory.data?.seats ?? emptySeats;
  const ownedSeats = seats.filter(seat => ownedIDs.includes(seat.id));
  const occupied = !!hold.pending || hold.hasRecord;
  const cancelled = hold.booking?.status === 'CANCELLED';
  const selected = useMemo(() => seats.filter(seat => selectedIDs.includes(seat.id) && seat.status === 'AVAILABLE'), [seats, selectedIDs]);
  const full = selected.length >= maxSeats;
  useEffect(() => { if (event.data) heading.current?.focus(); }, [event.data, heading]);
  // Drop selected seats that are no longer available, and say which ones.
  useEffect(() => {
    if (!inventory.data || occupied) return;
    const gone = selectedIDs.filter(seatID => !selected.some(seat => seat.id === seatID));
    if (!gone.length) return;
    select(selected.map(seat => seat.id));
    setLost(seats.filter(seat => gone.includes(seat.id)).map(seat => seat.label));
  }, [selectedIDs, inventory.data, occupied]);
  useEffect(() => { inventory.retry(); }, [hold.reservation?.status, hold.pending]);
  const groups = useMemo(() => groupSeats(seats), [seats]);
  const toggle = useCallback((seatID: string) => {
    setLost([]);
    if (selectedIDs.includes(seatID)) select(selectedIDs.filter(value => value !== seatID));
    else if (!full) select([...selected.map(seat => seat.id), seatID]);
  }, [selectedIDs, full, selected, select]);
  const total = (quote: { price_minor: number; currency: string }, n: number) => price({ price_minor: quote.price_minor * n, currency: quote.currency });
  const owned = ownedSeats.length || ownedIDs.length;

  return <>
    <a className="back-link" href="#">← All events</a>
    {event.error && <ErrorMessage message={event.error} retry={event.retry} />}
    {!event.data && !event.error && <p className="loading" role="status">Loading event…</p>}
    {event.data && <>
      <div className="page-heading"><p className="eyebrow">{date(event.data.event)}</p><h1 ref={heading} tabIndex={-1}>{event.data.event.title}</h1><p>{event.data.event.venue_name}</p>
        {session && <Interest eventID={event.data.event.id} userID={session.user.id} onSessionExpired={onSessionExpired} />}</div>
      <div className="event-layout">
        <section className="seat-panel" aria-labelledby="seat-heading">
          <div className="section-heading"><div><h2 id="seat-heading">Choose seats</h2><p>{occupied ? 'Your reservation appears in your selection.'
            : full ? `${maxSeats} of ${maxSeats} seats selected, the most one hold allows.`
            : selected.length ? `${selected.length} of up to ${maxSeats} seats selected.` : `Select up to ${maxSeats} available seats.`}</p></div><button className="button" aria-pressed={list} onClick={() => setList(value => !value)}>{list ? 'Seat map' : 'Seat list'}</button></div>
          {inventory.data && <SeatAvailability seats={inventory.data.seats} stale={!!inventory.error} />}
          <ul className="legend" aria-label="Seat availability"><li><span className="legend-mark" aria-hidden="true" />Available</li><li><span className="legend-mark selected-mark" aria-hidden="true">✓</span>Selected</li><li><span className="legend-mark held-mark" aria-hidden="true">–</span>Held</li><li><span className="legend-mark booked-mark" aria-hidden="true">✓</span>Booked</li><li><span className="legend-mark blocked-mark" aria-hidden="true">×</span>Blocked</li></ul>
          {inventory.error && <ErrorMessage message="Availability could not refresh. Retry before selecting seats." retry={inventory.retry} />}
          {!inventory.data && !inventory.error && <p className="loading" role="status">Loading seats…</p>}
          {inventory.data && <>
            {!list && seats.length > 0 && <div className="stage">Stage</div>}
            <SeatMap groups={groups} list={list} ownedIDs={ownedIDs} selected={selected} occupied={occupied} full={full}
              showOwnership={hold.reservation?.status === 'HELD' || (!!hold.booking && !cancelled) || !!hold.pending}
              booked={!!hold.booking} stale={!!inventory.error} onToggle={toggle} />
          </>}
        </section>
        <aside className="selection" aria-labelledby="selection-heading"><h2 id="selection-heading">Your selection</h2>
          <p className="price">{price(hold.booking ?? heldQuote ?? event.data.event)}<small>per seat</small></p>
          <div className="selection-detail">
            {hold.error && !(lost.length && hold.error === seatConflict) && <ErrorMessage message={hold.error} retry={hold.ready ? hold.retry : undefined} />}
            {hold.busy && <p role="status">{hold.pending?.kind === 'reserve' ? 'Reserving…' : hold.pending?.kind === 'confirm' ? 'Confirming…' : hold.pending?.kind === 'cancel' ? 'Cancelling…' : hold.pending?.kind === 'cancel_booking' ? 'Cancelling booking…' : 'Checking reservation…'}</p>}
            {hold.pending && !hold.busy && <p>Your saved {hold.pending.kind === 'reserve' ? 'reservation' : hold.pending.kind === 'confirm' ? 'confirmation' : hold.pending.kind === 'cancel_booking' ? 'booking cancellation' : 'cancellation'} request needs recovery before another action.</p>}
            {occupied && (ownedSeats.length ? <SeatSummary seats={ownedSeats} /> : <p className="selected-seat">Your {owned === 1 ? 'seat' : 'seats'}</p>)}
            {hold.booking && cancelled ? <div className="receipt" role="status">
              <h3>Booking cancelled</h3><p className="price">{total(hold.booking, hold.booking.seat_ids.length)}<small>for {seatCount(hold.booking.seat_ids.length)}, cancelled</small></p>
              <p>{hold.booking.cancelled_by_customer ? 'You' : 'The organizer'} cancelled this booking, and its seats went back on sale.</p>
              <dl><dt>Booking reference</dt><dd>{hold.booking.id}</dd>{hold.booking.cancelled_at && <><dt>Cancelled</dt><dd>{new Date(hold.booking.cancelled_at).toLocaleString()}</dd></>}</dl>
            </div> : hold.booking ? <div className="receipt" role="status">
              <h3>Booking confirmed</h3><p className="price">{total(hold.booking, hold.booking.seat_ids.length)}<small>for {seatCount(hold.booking.seat_ids.length)}</small></p>
              <dl><dt>Booking reference</dt><dd>{hold.booking.id}</dd><dt>Confirmed</dt><dd>{new Date(hold.booking.created_at).toLocaleString()}</dd></dl>
              <p>Keep this reference for your booking.</p>
              {/* The API decides whether the event has started, by its own clock. */}
              {confirmingCancel ? <p>Cancel this booking? Your seats go back on sale.{' '}
                <button className="text-button" disabled={hold.busy || !!hold.pending || !hold.ready} onClick={() => { setConfirmingCancel(false); hold.cancelBooking(); }}>Yes, cancel</button>{' '}
                <button className="text-button" onClick={() => setConfirmingCancel(false)}>Keep booking</button></p>
                : <button className="text-button" disabled={hold.busy || !!hold.pending || !hold.ready} onClick={() => setConfirmingCancel(true)}>Cancel booking…</button>}
            </div> : hold.reservation?.status === 'HELD' ? <>
              <p>{!hold.clockReady ? 'Checking your hold before confirmation.' : hold.seconds > 0 ? `Your ${owned === 1 ? 'seat is' : 'seats are'} held.` : `Your hold deadline has passed. Waiting for the server to release the ${owned === 1 ? 'seat' : 'seats'}.`}</p>
              {heldQuote && <p className="price">{total(heldQuote, hold.reservation.seat_ids.length)}<small>for {seatCount(hold.reservation.seat_ids.length)}</small></p>}
              {hold.seconds > 0 && <p className="countdown" role="timer" aria-label="Time remaining">{Math.floor(hold.seconds / 60)}:{String(hold.seconds % 60).padStart(2, '0')} <small>remaining</small></p>}
              <div className="reservation-actions"><button className="button button-solid" disabled={!hold.canConfirm} onClick={hold.confirm}>Confirm booking</button>
                <button className="button" disabled={hold.busy || !!hold.pending || !hold.ready} onClick={hold.cancel}>Cancel hold</button></div>
            </> : hold.reservation?.status === 'CONFIRMED' ? <p>Booking confirmed. Loading your receipt…</p>
              : hold.reservation && <p role="status">{hold.reservation.status === 'EXPIRED' ? 'Your hold expired. Choose available seats again.' : 'Your hold was cancelled.'}</p>}
            {hold.reservation && hold.reservation.status !== 'HELD' && !hold.pending && <button className="button" disabled={hold.busy || !hold.ready || (hold.reservation.status === 'CONFIRMED' && !hold.booking)} onClick={() => { hold.clear(); select([]); setLost([]); inventory.retry(); }}>Choose other seats</button>}
            {!occupied && <>
              {lost.length > 0 && <p className="error" role="status">{seatConflict} {lost.length === 1 ? `Seat ${lost[0]} was` : `Seats ${lost.join(', ')} were`} taken.{selected.length ? ` Your other ${selected.length === 1 ? 'seat is' : 'seats are'} still selected.` : ''}</p>}
              {selected.length ? <><SeatSummary seats={selected} />
                <p className="price">{total(event.data.event, selected.length)}<small>for {seatCount(selected.length)}</small></p>
                <p>A selection does not hold {selected.length === 1 ? 'the seat' : 'the seats'}.</p>
                <div className="reservation-actions"><button className="button button-solid" disabled={checking || hold.busy || !!inventory.error || (session !== null && !hold.ready)} onClick={() => { if (session) hold.reserve(selected.map(seat => seat.id)); else onLogin(); }}>{session ? `Reserve ${seatCount(selected.length)} for 5 min` : 'Log in to reserve'}</button>
                  <button className="button" onClick={() => { select([]); setLost([]); }}>Clear selection</button></div></>
                : !lost.length && <p>No seats selected yet.</p>}
            </>}
          </div>
        </aside>
      </div>
    </>}
  </>;
}
