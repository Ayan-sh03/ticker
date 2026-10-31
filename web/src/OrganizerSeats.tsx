/* Hallmark · component: organizer seat availability · genre: modern-minimal · theme: existing Coral tokens */
import { useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { ApiError, request, type OrganizerEvent, type Seat } from './api';
import { ErrorMessage } from './Feedback';
import { usePrivateResource } from './usePrivateResource';
import { groupSeats } from './seatGroups';

const emptySeats: Seat[] = [];

// One request changes at most this many seats, all or none.
const selectLimit = 200;
const statuses = { AVAILABLE: 'Available', HELD: 'Held', SOLD: 'Booked', BLOCKED: 'Blocked' };
const seatCount = (n: number) => `${n} ${n === 1 ? 'seat' : 'seats'}`;
const changeable = (seat: Seat) => seat.status === 'AVAILABLE' || seat.status === 'BLOCKED';

export function OrganizerSeats({ tenantID, eventID, userID, heading, onSessionExpired }: {
  tenantID: string; eventID: string; userID: string; heading: RefObject<HTMLHeadingElement | null>; onSessionExpired: () => void;
}) {
  const path = `/organizer/tenants/${tenantID}/events/${eventID}`;
  const event = usePrivateResource<{ event: OrganizerEvent }>(path, userID, onSessionExpired);
  const inventory = usePrivateResource<{ seats: Seat[] }>(`${path}/seats`, userID, onSessionExpired);
  const [selected, setSelected] = useState<string[]>([]);
  const [pending, setPending] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const mounted = useRef(true);
  // Only the latest refresh or change may update the map, so a slow response
  // cannot overwrite a newer one.
  const latest = useRef(0);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => { if (event.data) heading.current?.focus(); }, [event.data, heading]);

  const seats = inventory.data?.seats ?? emptySeats;
  const chosen = seats.filter(seat => selected.includes(seat.id) && changeable(seat));
  const toBlock = chosen.filter(seat => seat.status === 'AVAILABLE');
  const toUnblock = chosen.filter(seat => seat.status === 'BLOCKED');
  const full = chosen.length >= selectLimit;
  const counts = { AVAILABLE: 0, HELD: 0, SOLD: 0, BLOCKED: 0 };
  for (const seat of seats) counts[seat.status]++;
  const groups = useMemo(() => groupSeats(seats), [seats]);

  function failed(failure: unknown) {
    if (failure instanceof ApiError && failure.status === 401) { inventory.deny(); onSessionExpired(); return true; }
    if (failure instanceof ApiError && failure.status === 403) { inventory.deny(); return true; }
    return false;
  }

  // Refresh keeps the current map on screen instead of flashing a loading state.
  async function refresh() {
    const ticket = ++latest.current;
    try {
      const data = await request<{ seats: Seat[] }>(`${path}/seats`, { headers: { 'X-Ticker-User': userID } });
      if (!mounted.current || ticket !== latest.current) return;
      inventory.replace(data);
      setSelected(ids => ids.filter(id => data.seats.some(seat => seat.id === id && changeable(seat))));
    } catch (failure) {
      if (mounted.current && ticket === latest.current && !failed(failure)) setError('Seats could not refresh. Try again.');
    }
  }

  function toggle(ids: string[]) {
    setMessage(''); setError('');
    const allChosen = ids.every(id => selected.includes(id));
    if (allChosen) setSelected(selected.filter(id => !ids.includes(id)));
    else setSelected([...selected, ...ids.filter(id => !selected.includes(id))].slice(0, selectLimit));
  }

  async function change(status: 'BLOCKED' | 'AVAILABLE') {
    const targets = status === 'BLOCKED' ? toBlock : toUnblock;
    if (pending || !targets.length) return;
    setPending(true); setMessage(''); setError('');
    const ticket = ++latest.current;
    try {
      const result = await request<{ seats: Seat[] }>(`${path}/seats`, {
        method: 'PATCH', headers: { 'X-Ticker-User': userID },
        body: JSON.stringify({ seat_ids: targets.map(seat => seat.id), status }),
      });
      if (!mounted.current || ticket !== latest.current) return;
      const updated = new Map(result.seats.map(seat => [seat.id, seat]));
      inventory.replace({ seats: seats.map(seat => updated.get(seat.id) ?? seat) });
      setSelected(ids => ids.filter(id => !updated.has(id)));
      setMessage(`${status === 'BLOCKED' ? 'Blocked' : 'Unblocked'} ${seatCount(result.seats.length)}.`);
    } catch (failure) {
      if (!mounted.current || failed(failure)) return;
      if (failure instanceof ApiError && failure.code === 'seat_unavailable') {
        setError('Some selected seats were held or booked meanwhile, so nothing changed. Seats refreshed; check your selection.');
        await refresh();
      } else if (failure instanceof ApiError && failure.status < 500) setError(failure.message);
      // The change is idempotent, so retrying an unknown outcome is safe.
      else setError('The change may not have been saved. Try again.');
    } finally { if (mounted.current) setPending(false); }
  }

  const current = event.data?.event;
  return <>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Seats</h1><p>{current ? `${current.title} · Block seats to keep them off sale.` : 'Manage seat availability.'}</p></div>
    {(event.error || inventory.error) && <ErrorMessage message={event.error ?? inventory.error!} retry={() => { event.retry(); inventory.retry(); }} />}
    {(!current || !inventory.data) && !event.error && !inventory.error && <p className="loading" role="status">Loading seats…</p>}
    {current && inventory.data && (current.status === 'DRAFT' ? <p className="organizer-empty">Publish this event to create its seats.</p> : <div className="organizer-seats">
      <p className="availability-detail" role="status">{[
        `${counts.AVAILABLE} available`, `${counts.HELD} held`, `${counts.SOLD} booked`, `${counts.BLOCKED} blocked`,
      ].join(' · ')}</p>
      <div className="organizer-seat-actions" role="region" aria-label="Selected seats">
        <p>{chosen.length ? `${seatCount(chosen.length)} selected${full ? `, the most one change allows` : ''}.` : 'Select available seats to block, or blocked seats to unblock.'}</p>
        <div className="form-actions">
          <button className="button button-solid" disabled={pending || !toBlock.length} onClick={() => void change('BLOCKED')}>{pending && !refreshing ? 'Saving…' : `Block ${toBlock.length ? seatCount(toBlock.length) : 'seats'}`}</button>
          <button className="button" disabled={pending || !toUnblock.length} onClick={() => void change('AVAILABLE')}>Unblock {toUnblock.length ? seatCount(toUnblock.length) : 'seats'}</button>
          <button className="button" disabled={pending || !chosen.length} onClick={() => { setSelected([]); setMessage(''); setError(''); }}>Clear</button>
          <button className="button" disabled={pending} onClick={() => { setMessage(''); setError(''); setPending(true); setRefreshing(true); void refresh().finally(() => { if (mounted.current) { setPending(false); setRefreshing(false); } }); }}>{refreshing ? 'Refreshing…' : 'Refresh'}</button>
        </div>
        {message && <p role="status">{message}</p>}
        {error && <p className="error" role="alert">{error}</p>}
      </div>
      <ul className="legend" aria-label="Seat status"><li><span className="legend-mark" aria-hidden="true" />Available</li><li><span className="legend-mark selected-mark" aria-hidden="true">✓</span>Selected</li><li><span className="legend-mark held-mark" aria-hidden="true">–</span>Held</li><li><span className="legend-mark booked-mark" aria-hidden="true">✓</span>Booked</li><li><span className="legend-mark blocked-mark" aria-hidden="true">×</span>Blocked</li></ul>
      {[...groups].map(([group, rowSeats]) => {
        const rowIDs = rowSeats.filter(changeable).map(seat => seat.id);
        const rowChosen = rowIDs.length > 0 && rowIDs.every(id => selected.includes(id));
        return <div className="seat-group" key={group}>
          <div className="organizer-seat-row"><h3>{group}</h3>
            {rowIDs.length > 0 && <button className="text-button" disabled={pending || (!rowChosen && full)} onClick={() => toggle(rowIDs)}>{rowChosen ? 'Clear row' : 'Select row'}</button>}</div>
          <ul className="seat-grid">{rowSeats.map(seat => {
            const isChosen = selected.includes(seat.id) && changeable(seat);
            return <li key={seat.id}><button className="seat" data-seat-status={seat.status} aria-pressed={isChosen}
              disabled={pending || !changeable(seat) || (!isChosen && full)}
              aria-label={`${seat.label}, ${statuses[seat.status]}${isChosen ? ', selected' : ''}`} onClick={() => toggle([seat.id])}>
              <span>{seat.label}</span><span className="seat-state" aria-hidden="true">{isChosen ? '✓' : seat.status === 'AVAILABLE' ? '' : statuses[seat.status]}</span>
            </button></li>;
          })}</ul>
        </div>;
      })}
    </div>)}
  </>;
}
