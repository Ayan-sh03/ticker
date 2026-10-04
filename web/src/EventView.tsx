import { useEffect, useState, type RefObject } from 'react';
import { date, price, useResource, type Event, type Seat, type Session } from './api';
import { ErrorMessage } from './Feedback';
import { useReservation } from './useReservation';

const statuses = { AVAILABLE: 'Available', HELD: 'Held', SOLD: 'Booked', BLOCKED: 'Blocked' };
// Percentage of bookable seats remaining; blocked seats do not add capacity.
const availabilityThresholds = { fewPercent: 10, limitedPercent: 25 };

function SeatAvailability({ seats, stale }: { seats: Seat[]; stale: boolean }) {
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
}

export function EventView({ id, heading, session, checking, selectedID, select, onLogin, onSessionExpired }: {
  id: string; heading: RefObject<HTMLHeadingElement | null>; session: Session | null;
  checking: boolean; onLogin: () => void; onSessionExpired: () => void;
  selectedID: string | null; select: (value: string | null) => void;
}) {
  const event = useResource<{ event: Event }>(`/events/${encodeURIComponent(id)}`);
  const inventory = useResource<{ seats: Seat[] }>(event.data ? `/events/${encodeURIComponent(id)}/seats` : null, 2000);
  const [list, setList] = useState(false);
  const [lostSelection, setLostSelection] = useState(false);
  const hold = useReservation(id, session?.user.id, onSessionExpired);
  // Older replay responses may lack a quote; the fresh hold read supplies it
  // before confirmation becomes available.
  const heldQuote = hold.reservation?.price_minor !== undefined && hold.reservation.currency
    ? { price_minor: hold.reservation.price_minor, currency: hold.reservation.currency } : undefined;
  const ownedID = hold.booking?.seat_id ?? hold.reservation?.seat_id ?? (hold.pending?.kind === 'reserve' ? hold.pending.seatID : undefined);
  const ownedSeat = inventory.data?.seats.find(seat => seat.id === ownedID);
  const occupied = !!hold.pending || hold.hasRecord;
  const selected = inventory.data?.seats.find(seat => seat.id === selectedID && seat.status === 'AVAILABLE');
  useEffect(() => { if (event.data) heading.current?.focus(); }, [event.data, heading]);
  useEffect(() => {
    if (selectedID && inventory.data && !selected && selectedID !== ownedID) { select(null); setLostSelection(true); }
  }, [selectedID, inventory.data, selected, ownedID]);
  useEffect(() => { inventory.retry(); }, [hold.reservation?.status, hold.pending]);
  const groups = new Map<string, Seat[]>();
  for (const seat of inventory.data?.seats ?? []) {
    const group = `${seat.section} / Row ${seat.row_number}`;
    groups.set(group, [...(groups.get(group) ?? []), seat]);
  }

  return <>
    <a className="back-link" href="#">← All events</a>
    {event.error && <ErrorMessage message={event.error} retry={event.retry} />}
    {!event.data && !event.error && <p className="loading" role="status">Loading event…</p>}
    {event.data && <>
      <div className="page-heading"><p className="eyebrow">{date(event.data.event)}</p><h1 ref={heading} tabIndex={-1}>{event.data.event.title}</h1><p>{event.data.event.venue_name}</p></div>
      <div className="event-layout">
        <section className="seat-panel" aria-labelledby="seat-heading">
          <div className="section-heading"><div><h2 id="seat-heading">Choose a seat</h2><p>{occupied ? 'Your reservation appears in your selection.' : selected ? `Seat ${selected.label} selected.` : 'Select one available seat.'}</p></div><button className="button" aria-pressed={list} onClick={() => setList(value => !value)}>{list ? 'Seat map' : 'Seat list'}</button></div>
          {inventory.data && <SeatAvailability seats={inventory.data.seats} stale={!!inventory.error} />}
          <ul className="legend" aria-label="Seat availability"><li><span className="legend-mark" aria-hidden="true" />Available</li><li><span className="legend-mark selected-mark" aria-hidden="true">✓</span>Selected</li><li><span className="legend-mark held-mark" aria-hidden="true">–</span>Held</li><li><span className="legend-mark booked-mark" aria-hidden="true">✓</span>Booked</li><li><span className="legend-mark blocked-mark" aria-hidden="true">×</span>Blocked</li></ul>
          {inventory.error && <ErrorMessage message="Availability could not refresh. Retry before selecting a seat." retry={inventory.retry} />}
          {!inventory.data && !inventory.error && <p className="loading" role="status">Loading seats…</p>}
          {inventory.data && <>
            {!list && inventory.data.seats.length > 0 && <div className="stage">Stage</div>}
            {[...groups].map(([group, seats]) => <div className="seat-group" key={group}>
              <h3>{group}</h3><ul className={list ? 'seat-list' : 'seat-grid'}>{seats.map(seat => {
                const unavailable = seat.status !== 'AVAILABLE';
                const yours = seat.id === ownedID && (hold.reservation?.status === 'HELD' || !!hold.booking || !!hold.pending);
                const chosen = !occupied && seat.id === selected?.id;
                return <li key={seat.id}><button className={`seat ${list ? 'seat-line' : ''} ${yours ? 'seat-owned' : ''}`} data-seat-status={seat.status} disabled={unavailable || !!inventory.error || occupied} aria-pressed={chosen || yours} aria-label={`${seat.label}, ${yours ? hold.booking ? 'your confirmed seat' : 'your reservation' : statuses[seat.status]}${chosen ? ', selected' : ''}`} onClick={() => { select(chosen ? null : seat.id); setLostSelection(false); }}>
                  <span>{seat.label}</span><span className="seat-state" aria-hidden="true">{yours ? list ? 'Yours' : '✓' : list || unavailable ? statuses[seat.status] : chosen ? '✓' : ''}</span>
                </button></li>;
              })}</ul>
            </div>)}
          </>}
        </section>
        <aside className="selection" aria-labelledby="selection-heading"><h2 id="selection-heading">Your selection</h2>
          <p className="price">{price(hold.booking ?? heldQuote ?? event.data.event)}<small>per seat</small></p>
          <div className="selection-detail">
            {hold.error && <ErrorMessage message={hold.error} retry={hold.ready ? hold.retry : undefined} />}
            {hold.busy && <p role="status">{hold.pending?.kind === 'reserve' ? 'Reserving…' : hold.pending?.kind === 'confirm' ? 'Confirming…' : hold.pending?.kind === 'cancel' ? 'Cancelling…' : 'Checking reservation…'}</p>}
            {hold.pending && !hold.busy && <p>Your saved {hold.pending.kind === 'reserve' ? 'reservation' : hold.pending.kind === 'confirm' ? 'confirmation' : 'cancellation'} request needs recovery before another action.</p>}
            {occupied && <p className="selected-seat">{ownedSeat ? `Seat ${ownedSeat.label}` : 'Your seat'}{ownedSeat && <small>{ownedSeat.section} · Row {ownedSeat.row_number}</small>}</p>}
            {hold.booking ? <div className="receipt" role="status">
              <h3>Booking confirmed</h3><p className="price">{price(hold.booking)}</p>
              <dl><dt>Booking reference</dt><dd>{hold.booking.id}</dd><dt>Confirmed</dt><dd>{new Date(hold.booking.created_at).toLocaleString()}</dd></dl>
              <p>Keep this reference for your booking.</p>
            </div> : hold.reservation?.status === 'HELD' ? <>
              <p>{!hold.clockReady ? 'Checking your hold before confirmation.' : hold.seconds > 0 ? 'Your seat is held.' : 'Your hold deadline has passed. Waiting for the server to release the seat.'}</p>
              {hold.seconds > 0 && <p className="countdown" role="timer" aria-label="Time remaining">{Math.floor(hold.seconds / 60)}:{String(hold.seconds % 60).padStart(2, '0')} <small>remaining</small></p>}
              <div className="reservation-actions"><button className="button button-solid" disabled={!hold.canConfirm} onClick={hold.confirm}>Confirm booking</button>
                <button className="button" disabled={hold.busy || !!hold.pending || !hold.ready} onClick={hold.cancel}>Cancel hold</button></div>
            </> : hold.reservation?.status === 'CONFIRMED' ? <p>Booking confirmed. Loading your receipt…</p>
              : hold.reservation && <p role="status">{hold.reservation.status === 'EXPIRED' ? 'Your hold expired. Choose another available seat.' : 'Your hold was cancelled.'}</p>}
            {hold.reservation && hold.reservation.status !== 'HELD' && !hold.pending && <button className="button" disabled={hold.busy || !hold.ready || (hold.reservation.status === 'CONFIRMED' && !hold.booking)} onClick={() => { hold.clear(); select(null); setLostSelection(false); inventory.retry(); }}>Choose another seat</button>}
            {!occupied && (selected ? <><p className="selected-seat">Seat {selected.label}</p><p>{selected.section} · Row {selected.row_number}</p><p>A selection does not hold the seat.</p>
              <div className="reservation-actions"><button className="button button-solid" disabled={checking || hold.busy || !!inventory.error || (session !== null && !hold.ready)} onClick={() => { if (session) hold.reserve(selected.id); else onLogin(); }}>{session ? 'Reserve for 5 min' : 'Log in to reserve'}</button>
                <button className="button" onClick={() => select(null)}>Clear selection</button></div></>
              : <p>{lostSelection ? 'That seat is no longer available. Choose another.' : 'No seat selected yet.'}</p>
            )}
          </div>
        </aside>
      </div>
    </>}
  </>;
}
