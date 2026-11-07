import { memo, useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { ApiError, date, price, request, sale, useResource, when, type Booking, type Event, type Seat, type Session } from './api';
import { ErrorMessage } from './Feedback';
import { maxSeats, seatConflict, useReservation } from './useReservation';
import { groupSeats } from './seatGroups';
import { SeatLegend, SeatMap, type SeatLook, type SeatTone } from './SeatMap';
import { useEventBookings } from './useEventBookings';

const emptySeats: Seat[] = [];
const emptyIDs: string[] = [];
const emptyBookings: Booking[] = [];

// Customers see one unavailable look: a blocked seat is as out of reach as a sold one.
const statuses = { AVAILABLE: 'Available', HELD: 'Held, may free up', SOLD: 'Unavailable', BLOCKED: 'Unavailable' };
const tones: Record<Seat['status'], SeatTone> = { AVAILABLE: 'available', HELD: 'held', SOLD: 'booked', BLOCKED: 'booked' };
const legend: [SeatTone, string][] = [['available', 'Available'], ['selected', 'Your seats'], ['held', 'Held, may free up'], ['booked', 'Unavailable']];
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
  ].filter(item => item.count).map(item => `${item.count} ${item.label}`).join(' · ');
  return <div className="availability-summary" role="status" aria-atomic="true">
    <div className="availability-heading"><p className="remaining-count"><strong>{counts.AVAILABLE}</strong> {counts.AVAILABLE === 1 ? 'seat' : 'seats'} remaining</p><span className="availability-label" data-level={level}>{label}</span></div>
    {detail && <p className="availability-detail">{detail}</p>}
  </div>;
});

// "Row 2 · Seats 4, 5", led by the section when the room has several.
const rowLines = (seats: Seat[], sections: boolean) => [...groupSeats(seats)].map(([key, row]) => ({ key,
  text: `${sections ? `${row[0].section} · ` : ''}Row ${row[0].row_number} · ${row.length === 1 ? 'Seat' : 'Seats'} ${row.map(seat => seat.seat_number).join(', ')}` }));

function SeatSummary({ seats, sections }: { seats: Seat[]; sections: boolean }) {
  return <ul className="seat-summary">{rowLines(seats, sections).map(line => <li key={line.key} className="selected-seat">{line.text}</li>)}</ul>;
}

// Active bookings for this event from any tab or device; the one this tab shows is left out.
function MyBookings({ bookings, seats, sections, count, total, other, disabled, onView }: {
  bookings: Booking[]; seats: Seat[]; sections: boolean; count: number; total: number; other: boolean; disabled: boolean; onView: (booking: Booking) => void;
}) {
  return <section className="my-bookings" aria-labelledby="my-bookings-heading">
    <h2 id="my-bookings-heading">{other ? `You also have ${seatCount(count)} booked` : `You have ${seatCount(count)} booked for this event`}</h2>
    <p>{bookings.length} {bookings.length === 1 ? 'booking' : 'bookings'} on this page · {total} active in total{other ? ' including the receipt below' : ''}.</p>
    <ul>{bookings.map(booking => {
      const own = seats.filter(seat => booking.seat_ids.includes(seat.id));
      return <li key={booking.id}><p><span>{own.length === booking.seat_ids.length ? rowLines(own, sections).map(line => line.text).join('; ') : seatCount(booking.seat_ids.length)}</span>
        <small>{price({ price_minor: booking.price_minor * booking.seat_ids.length, currency: booking.currency })} · Booked {when(booking.created_at)}</small></p>
        <button className="button" disabled={disabled} onClick={() => onView(booking)}>View receipt</button></li>;
    })}</ul>
  </section>;
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
  return <p className="interest">{interested ? <><span className="interest-state">Marked as interested</span>
    <button className="text-button" disabled={busy} onClick={() => void toggle()}>Remove</button></>
    : <button className="button" disabled={busy} onClick={() => void toggle()}>Mark as interested</button>}
    {failure === 'save' && <span className="error" role="alert">Your interest could not be saved. Try again.</span>}</p>;
}

// The text alternative to the map. Countdown ticks leave this subtree untouched.
const SeatList = memo(function SeatList({ groups, ownedIDs, bookedIDs, selected, occupied, full, showOwnership, booked, stale, onToggle }: {
  groups: Map<string, Seat[]>; ownedIDs: string[]; bookedIDs: string[]; selected: Seat[];
  occupied: boolean; full: boolean; showOwnership: boolean; booked: boolean; stale: boolean; onToggle: (id: string) => void;
}) {
  return <>
    {[...groups].map(([group, rowSeats]) => <div className="seat-group" key={group}>
      <h3>{group}</h3><ul className="seat-list">{rowSeats.map(seat => {
        const unavailable = seat.status !== 'AVAILABLE';
        const elsewhere = seat.status === 'SOLD' && bookedIDs.includes(seat.id);
        const yours = (ownedIDs.includes(seat.id) && showOwnership) || elsewhere;
        const chosen = !occupied && selected.some(value => value.id === seat.id);
        const capped = !unavailable && !chosen && !occupied && full;
        return <li key={seat.id}><button className={`seat seat-line ${yours ? 'seat-owned' : ''} ${capped ? 'seat-capped' : ''}`} data-seat-status={seat.status === 'BLOCKED' ? 'SOLD' : seat.status} disabled={unavailable || stale || occupied || capped} aria-pressed={chosen || yours} aria-label={`${seat.label}, ${yours ? booked || elsewhere ? 'your booked seat' : 'your reservation' : statuses[seat.status]}${chosen ? ', selected' : ''}${capped ? `, ${maxSeats}-seat limit reached` : ''}`} onClick={() => onToggle(seat.id)}>
          <span>{seat.label}</span><span className="seat-state" aria-hidden="true">{yours ? 'Yours' : chosen ? 'Selected' : statuses[seat.status]}</span>
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
  const [cancelBookingID, setCancelBookingID] = useState<string>();
  const [bookingPages, setBookingPages] = useState(['']);
  // Why the map was handed back after a hold ended, shown until the next change.
  const [notice, setNotice] = useState('');
  // Seats of a hold that ended, selected again once a fresh seat read arrives.
  const restore = useRef<{ ids: string[]; lead: string; since: number } | null>(null);
  const hold = useReservation(id, session?.user.id, onSessionExpired);
  const receiptKey = hold.booking ? `${hold.booking.id}:${hold.booking.status}` : '';
  const mine = useEventBookings(event.data ? id : null, session?.user.id, bookingPages[bookingPages.length - 1], receiptKey, onSessionExpired);
  useEffect(() => { setCancelBookingID(undefined); }, [receiptKey]);
  const otherBookings = useMemo(() => (mine.data?.bookings ?? emptyBookings)
    .filter(booking => booking.id.toLowerCase() !== hold.booking?.id.toLowerCase()), [mine.data, hold.booking?.id]);
  const bookedIDs = mine.error ? emptyIDs : mine.data?.booked_seat_ids ?? emptyIDs;
  const otherBookedCount = (mine.data?.booked_seat_ids ?? emptyIDs)
    .filter(seatID => hold.booking?.status === 'CANCELLED' || !hold.booking?.seat_ids.includes(seatID)).length;
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
  // An expired or cancelled hold hands the map back on its own. A booking's
  // cancellation keeps its receipt instead.
  useEffect(() => {
    const ended = hold.reservation;
    if (!ended || (ended.status !== 'EXPIRED' && ended.status !== 'CANCELLED') || hold.booking || hold.pending || hold.busy || !hold.ready) return;
    const lead = ended.status === 'EXPIRED' ? 'Your hold expired.' : 'Your hold was cancelled.';
    restore.current = { ids: ended.seat_ids, lead, since: performance.now() };
    // Drop the old selection first, so a lagging read cannot report these seats as taken.
    select([]); setLost([]); setNotice(lead);
    hold.clear();
  }, [hold.reservation?.status, hold.booking, hold.pending, hold.busy, hold.ready]);
  // The public seat map may lag the release by its two-second cache; wait for a
  // read that no longer shows the seats held, for up to five seconds.
  useEffect(() => {
    const pending = restore.current;
    if (!pending || occupied || !inventory.data) return;
    const current = inventory.data.seats.filter(seat => pending.ids.includes(seat.id));
    if (current.some(seat => seat.status === 'HELD') && performance.now() - pending.since < 5000) return;
    restore.current = null;
    const free = current.filter(seat => seat.status === 'AVAILABLE');
    select(free.map(seat => seat.id));
    setLost([]);
    const lead = pending.lead;
    setNotice(free.length === pending.ids.length ? `${lead} Its ${free.length === 1 ? 'seat is' : 'seats are'} selected again.`
      : free.length ? `${lead} ${free.length} of its seats are selected again; the others are no longer available.`
      : `${lead} Its seats are no longer available.`);
  }, [inventory.data, occupied]);
  const groups = useMemo(() => groupSeats(seats), [seats]);
  const sections = useMemo(() => new Set(seats.map(seat => seat.section)).size > 1, [seats]);
  const toggle = useCallback((seatID: string) => {
    setLost([]); setNotice('');
    if (selectedIDs.includes(seatID)) select(selectedIDs.filter(value => value !== seatID));
    else if (!full) select([...selected.map(seat => seat.id), seatID]);
  }, [selectedIDs, full, selected, select]);
  const showOwnership = hold.reservation?.status === 'HELD' || (!!hold.booking && !cancelled) || !!hold.pending;
  const booked = !!hold.booking;
  const stale = !!inventory.error;
  const onSale = event.data ? sale(event.data.event) : 'open';
  // During a hold every seat keeps its true state; only yours turn coral.
  const look = useCallback((seat: Seat): SeatLook => {
    if (showOwnership && ownedIDs.includes(seat.id)) return { tone: 'selected', enabled: false, label: `${seat.label}, ${booked ? 'your booked seat' : 'your reservation'}` };
    if (seat.status === 'SOLD' && bookedIDs.includes(seat.id)) return { tone: 'selected', enabled: false, label: `${seat.label}, your booked seat` };
    const chosen = !occupied && selected.some(value => value.id === seat.id);
    const capped = seat.status === 'AVAILABLE' && !chosen && !occupied && full;
    return {
      tone: chosen ? 'selected' : tones[seat.status],
      enabled: seat.status === 'AVAILABLE' && !stale && !occupied && !capped && onSale === 'open',
      label: `${seat.label}, ${statuses[seat.status]}${chosen ? ', selected' : ''}${capped ? `, ${maxSeats}-seat limit reached` : ''}`,
    };
  }, [showOwnership, ownedIDs, bookedIDs, booked, occupied, selected, full, stale, onSale]);
  const total = (quote: { price_minor: number; currency: string }, n: number) => price({ price_minor: quote.price_minor * n, currency: quote.currency });
  const owned = ownedSeats.length || ownedIDs.length;
  const holding = hold.reservation?.status === 'HELD';
  const heroCount = hold.booking ? hold.booking.seat_ids.length : holding ? hold.reservation!.seat_ids.length : !occupied ? selected.length : 0;
  const heroQuote = hold.booking ?? (holding ? heldQuote : undefined) ?? event.data?.event;
  const reserveDisabled = checking || hold.busy || !!inventory.error || onSale !== 'open' || (session !== null && !hold.ready);
  const reserve = () => { setNotice(''); if (session) hold.reserve(selected.map(seat => seat.id)); else onLogin(); };
  const clock = `${Math.floor(hold.seconds / 60)}:${String(hold.seconds % 60).padStart(2, '0')}`;
  const title = hold.booking ? 'Your booking' : hold.reservation || hold.pending ? 'Your hold' : 'Your selection';
  const ended = onSale === 'started' && !occupied;
  const viewReceipt = (booking: Booking) => { setCancelBookingID(undefined); setNotice(''); setLost([]); hold.view(booking); document.getElementById('your-selection')?.focus(); };

  return <>
    <a className="back-link" href="#">← All events</a>
    {event.error && <ErrorMessage message={event.error} retry={event.retry} />}
    {!event.data && !event.error && <p className="loading" role="status">Loading event…</p>}
    {event.data && <>
      <div className="page-heading"><h1 ref={heading} tabIndex={-1}>{event.data.event.title}</h1><p>{date(event.data.event)} · {event.data.event.venue_name}</p>
        {session && onSale !== 'started' && <Interest eventID={event.data.event.id} userID={session.user.id} onSessionExpired={onSessionExpired} />}</div>
      {mine.data && (otherBookings.length > 0 || mine.data.next_before || bookingPages.length > 1) && <MyBookings bookings={otherBookings} seats={seats} sections={sections}
        count={otherBookedCount} total={mine.data.total_bookings} other={!!hold.booking && !cancelled}
        disabled={hold.busy || !!hold.pending || holding || !hold.ready} onView={viewReceipt} />}
      {session && (bookingPages.length > 1 || mine.data?.next_before) && <nav className="pagination" aria-label="Your booking pages">
        <button className="button" disabled={bookingPages.length === 1 || (mine.loading && !mine.data)} onClick={() => setBookingPages(pages => pages.slice(0, -1))}>Previous</button>
        <button className="button" disabled={!mine.data?.next_before || !!mine.error} onClick={() => { if (mine.data?.next_before) setBookingPages(pages => [...pages, mine.data!.next_before!]); }}>Older bookings</button>
      </nav>}
      {session && mine.loading && !mine.data && <p className="loading" role="status">Loading your bookings…</p>}
      {mine.error && <p className="error" role="status">{mine.error} <button className="text-button" onClick={mine.retry}>Retry</button></p>}
      {ended ? <section className="event-ended" aria-labelledby="ended-heading"><h2 id="ended-heading">This event has started</h2><p>Its seats are no longer on sale.</p></section>
        : <div className="event-layout">
        <section className="seat-panel" aria-labelledby="seat-heading">
          <div className="section-heading"><div><h2 id="seat-heading">Choose seats</h2><p aria-live="polite">{occupied ? hold.booking && !cancelled ? 'Your booked seats are marked on the map.'
            : hold.reservation?.status === 'HELD' || hold.pending ? 'Your held seats are marked on the map. Confirm or cancel the hold to change seats.' : 'Your reservation appears in your selection.'
            : onSale === 'upcoming' ? 'Preview the room before sales open.'
            : full ? `${maxSeats} of ${maxSeats} seats selected, the most one hold allows.`
            : selected.length ? `${selected.length} of up to ${maxSeats} seats selected.` : `Select up to ${maxSeats} available seats.`}</p></div>
            <div className="segmented" role="group" aria-label="Seat view"><button className="button" aria-pressed={!list} onClick={() => setList(false)}>Map</button><button className="button" aria-pressed={list} onClick={() => setList(true)}>List</button></div></div>
          {inventory.data && <SeatAvailability seats={inventory.data.seats} stale={!!inventory.error} />}
          {inventory.error && <ErrorMessage message="Availability could not refresh. Retry before selecting seats." retry={inventory.retry} />}
          {!inventory.data && !inventory.error && <p className="loading" role="status">Loading seats…</p>}
          {inventory.data && <>
            <a className="skip-inline" href="#your-selection" onClick={click => { click.preventDefault(); document.getElementById('your-selection')?.focus(); }}>Skip to your selection</a>
            {list ? <SeatList groups={groups} ownedIDs={ownedIDs} bookedIDs={bookedIDs} selected={selected} occupied={occupied} full={full}
              showOwnership={showOwnership} booked={booked} stale={stale || onSale !== 'open'} onToggle={toggle} />
              : seats.length > 0 && <SeatMap seats={seats} look={look} onToggle={toggle} sectionHeading="h3"
                hint={onSale === 'open' ? 'Use the arrow keys to move between seats, and Space to select one.' : 'Seats cannot be selected until sales open.'} />}
            <SeatLegend label="Seat availability" items={legend} />
          </>}
          {/* On narrow screens the panel sits below the map; this bar keeps the next step in reach. */}
          {holding && hold.seconds > 0 ? <div className="selection-bar" role="region" aria-label="Your hold">
            <p><strong>{total(heroQuote!, heroCount)}</strong>{seatCount(heroCount)} · <span data-urgent={hold.seconds <= 60 || undefined}>{clock} left</span></p>
            <button className="button button-solid" disabled={!hold.canConfirm} onClick={hold.confirm}>Confirm</button>
          </div> : !occupied && selected.length > 0 && <div className="selection-bar" role="region" aria-label="Your selection summary">
            <p><strong>{total(event.data.event, selected.length)}</strong>{seatCount(selected.length)}</p>
            <button className="button button-solid" disabled={reserveDisabled} onClick={reserve}>{session ? 'Reserve for 5 min' : 'Log in to reserve'}</button>
          </div>}
        </section>
        <aside className="selection" id="your-selection" tabIndex={-1} aria-labelledby="selection-heading"><h2 id="selection-heading">{title}</h2>
          <div className="selection-status">
            {hold.error && !(lost.length && hold.error === seatConflict) && <ErrorMessage message={hold.error} retry={hold.ready ? hold.retry : undefined} />}
            {hold.busy && <p role="status">{hold.pending?.kind === 'reserve' ? 'Reserving…' : hold.pending?.kind === 'confirm' ? 'Confirming…' : hold.pending?.kind === 'cancel' ? 'Cancelling…' : hold.pending?.kind === 'cancel_booking' ? 'Cancelling booking…' : 'Checking reservation…'}</p>}
            {hold.pending && !hold.busy && <p>Your last {hold.pending.kind === 'reserve' ? 'reservation' : hold.pending.kind === 'confirm' ? 'confirmation' : hold.pending.kind === 'cancel_booking' ? 'booking cancellation' : 'cancellation'} request did not finish. Retry it before doing anything else.</p>}
          </div>
          {/* During a hold the clock and the decision come first, so they stay in view. */}
          {holding && <div className="hold-clock">
            {hold.seconds > 0 && <p className="countdown" data-urgent={hold.seconds <= 60 || undefined} role="timer" aria-label="Time remaining">{clock} <small>remaining</small></p>}
            <p>{!hold.clockReady ? 'Checking your hold before confirmation.' : hold.seconds > 0 ? `${hold.seconds <= 60 ? 'Less than a minute left. ' : ''}Confirm before the timer ends, or the ${owned === 1 ? 'seat goes' : 'seats go'} back on sale.`
              : `Your hold deadline has passed. Waiting for the server to release the ${owned === 1 ? 'seat' : 'seats'}.`}</p>
            {/* The timer itself is silent; this announces the two warnings once each. */}
            <p className="visually-hidden" aria-live="polite">{hold.seconds > 0 && hold.seconds <= 15 ? '15 seconds left on your hold.' : hold.seconds > 0 && hold.seconds <= 60 ? 'Less than a minute left on your hold.' : ''}</p>
            <div className="reservation-actions"><button className="button button-solid" disabled={!hold.canConfirm} onClick={hold.confirm}>Confirm booking</button>
              <button className="button" disabled={hold.busy || !!hold.pending || !hold.ready} onClick={hold.cancel}>Cancel hold</button></div>
          </div>}
          {/* The total leads once there are seats to pay for; the seat price follows it. */}
          <p className="price selection-total">{heroCount ? total(heroQuote!, heroCount) : price(heroQuote!)}<small>{heroCount ? `${seatCount(heroCount)} · ${price(heroQuote!)} each${cancelled ? ' · cancelled' : ''}` : 'per seat'}</small></p>
          <div className="selection-detail">
            {hold.booking && <h3 className="receipt-title">{cancelled ? 'Booking cancelled' : 'Booking confirmed'}</h3>}
            {occupied && (ownedSeats.length ? <SeatSummary seats={ownedSeats} sections={sections} /> : <p className="selected-seat">Your {owned === 1 ? 'seat' : 'seats'}</p>)}
            {hold.booking && cancelled ? <div className="receipt" role="status">
              <p>{hold.booking.cancelled_by_customer ? 'You' : 'The organizer'} cancelled this booking, and its seats went back on sale.</p>
              <dl><dt>Booking reference</dt><dd>{hold.booking.id}</dd>{hold.booking.cancelled_at && <><dt>Cancelled</dt><dd>{new Date(hold.booking.cancelled_at).toLocaleString()}</dd></>}</dl>
            </div> : hold.booking ? <div className="receipt" role="status">
              <dl><dt>Booking reference</dt><dd>{hold.booking.id}</dd><dt>Confirmed</dt><dd>{new Date(hold.booking.created_at).toLocaleString()}</dd></dl>
              {/* The API decides whether the event has started, by its own clock. */}
              {cancelBookingID === hold.booking.id ? <div className="confirm-cancel">
                <p>Cancel your booking for {ownedSeats.length ? rowLines(ownedSeats, sections).map(line => line.text).join('; ') : seatCount(owned)}? {owned === 1 ? 'The seat goes' : 'The seats go'} back on sale, and this cannot be undone.</p>
                <div className="reservation-actions"><button className="button" autoFocus onClick={() => setCancelBookingID(undefined)}>Keep booking</button>
                  <button className="button button-danger" disabled={hold.busy || !!hold.pending || !hold.ready} onClick={() => { setCancelBookingID(undefined); hold.cancelBooking(); }}>Cancel booking</button></div>
              </div>
                : <button className="text-button" disabled={hold.busy || !!hold.pending || !hold.ready} onClick={() => setCancelBookingID(hold.booking!.id)}>Cancel booking…</button>}
            </div> : holding ? null : hold.reservation?.status === 'CONFIRMED' ? <p>Booking confirmed. Loading your receipt…</p>
              : hold.reservation && <p role="status">{hold.reservation.status === 'EXPIRED' ? 'Your hold expired. Choose available seats again.' : 'Your hold was cancelled.'}</p>}
            {hold.reservation && hold.reservation.status !== 'HELD' && !hold.pending && <button className="button" disabled={hold.busy || !hold.ready || (hold.reservation.status === 'CONFIRMED' && !hold.booking)} onClick={() => { setCancelBookingID(undefined); hold.clear(); select([]); setLost([]); inventory.retry(); }}>Choose other seats</button>}
            {!occupied && <>
              {notice && <p role="status">{notice}</p>}
              {lost.length > 0 && <p className="error" role="status">{seatConflict} {lost.length === 1 ? `Seat ${lost[0]} was` : `Seats ${lost.join(', ')} were`} taken.{selected.length ? ` Your other ${selected.length === 1 ? 'seat is' : 'seats are'} still selected.` : ''}</p>}
              {selected.length ? <><SeatSummary seats={selected} sections={sections} />
                <p>A selection does not hold {selected.length === 1 ? 'the seat' : 'the seats'}.</p>
                <div className="reservation-actions"><button className="button button-solid" disabled={reserveDisabled} onClick={reserve}>{session ? `Reserve ${seatCount(selected.length)} for 5 min` : 'Log in to reserve'}</button>
                  <button className="button" onClick={() => { select([]); setLost([]); setNotice(''); }}>Clear selection</button></div></>
                : !lost.length && <p>{onSale === 'upcoming' ? `Seats go on sale ${when(event.data.event.sales_open_at)}.` : 'No seats selected yet.'}</p>}
            </>}
          </div>
        </aside>
      </div>}
    </>}
  </>;
}
