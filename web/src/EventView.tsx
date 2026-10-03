import { useEffect, useState, type RefObject } from 'react';
import { date, price, useResource, type Event, type Seat } from './api';
import { ErrorMessage } from './Feedback';

const statuses = { AVAILABLE: 'Available', HELD: 'Held', SOLD: 'Sold', BLOCKED: 'Blocked' };

export function EventView({ id, heading }: { id: string; heading: RefObject<HTMLHeadingElement | null> }) {
  const event = useResource<{ event: Event }>(`/events/${encodeURIComponent(id)}`);
  const inventory = useResource<{ seats: Seat[] }>(event.data ? `/events/${encodeURIComponent(id)}/seats` : null, 2000);
  const [selectedID, select] = useState<string | null>(null);
  const [list, setList] = useState(false);
  const [lostSelection, setLostSelection] = useState(false);
  const selected = inventory.data?.seats.find(seat => seat.id === selectedID && seat.status === 'AVAILABLE');
  useEffect(() => { if (event.data) heading.current?.focus(); }, [event.data, heading]);
  useEffect(() => {
    if (selectedID && inventory.data && !selected) { select(null); setLostSelection(true); }
  }, [selectedID, inventory.data, selected]);
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
          <div className="section-heading"><div><h2 id="seat-heading">Choose a seat</h2><p>{selected ? `Seat ${selected.label} selected.` : 'Select one available seat.'}</p></div><button className="button" aria-pressed={list} onClick={() => setList(value => !value)}>{list ? 'Seat map' : 'Seat list'}</button></div>
          <ul className="legend" aria-label="Seat availability"><li><span className="legend-mark" />Available</li><li><span className="legend-mark selected-mark">✓</span>Selected</li><li><span className="legend-mark unavailable-mark">×</span>Unavailable</li></ul>
          {inventory.error && <ErrorMessage message="Availability could not refresh. Retry before selecting a seat." retry={inventory.retry} />}
          {!inventory.data && !inventory.error && <p className="loading" role="status">Loading seats…</p>}
          {inventory.data && <>
            {inventory.data.seats.length === 0 && <p>No seats are listed for this event.</p>}
            {!list && <div className="stage">Stage</div>}
            {[...groups].map(([group, seats]) => <div className="seat-group" key={group}>
              <h3>{group}</h3><ul className={list ? 'seat-list' : 'seat-grid'}>{seats.map(seat => {
                const unavailable = seat.status !== 'AVAILABLE';
                const chosen = seat.id === selected?.id;
                return <li key={seat.id}><button className={`seat ${list ? 'seat-line' : ''}`} disabled={unavailable || !!inventory.error} aria-pressed={chosen} aria-label={`${seat.label}, ${statuses[seat.status]}${chosen ? ', selected' : ''}`} onClick={() => { select(chosen ? null : seat.id); setLostSelection(false); }}>
                  <span>{seat.label}</span><span className="seat-state">{list ? statuses[seat.status] : chosen ? '✓' : unavailable ? '×' : ''}</span>
                </button></li>;
              })}</ul>
            </div>)}
          </>}
        </section>
        <aside className="selection" aria-labelledby="selection-heading"><h2 id="selection-heading">Your selection</h2>
          <p className="price">{price(event.data.event)}<small>per seat</small></p>
          <div className="selection-detail" aria-live="polite">
            {selected ? <><p className="selected-seat">Seat {selected.label}</p><p>{selected.section} · Row {selected.row_number}</p><p>A selection does not hold the seat.</p><button className="button" onClick={() => select(null)}>Clear selection</button></>
              : <p>{lostSelection ? 'That seat is no longer available. Choose another.' : 'No seat selected yet.'}</p>}
          </div>
        </aside>
      </div>
    </>}
  </>;
}
