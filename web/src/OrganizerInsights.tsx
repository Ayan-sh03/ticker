/* Hallmark · component: organizer insights · genre: modern-minimal · theme: existing Coral tokens */
import { useEffect, type RefObject } from 'react';
import { ErrorMessage } from './Feedback';
import { usePrivateResource } from './usePrivateResource';

// Money arrives as exact decimal strings of minor units; sums can exceed what a JavaScript number holds.
type Revenue = { bookings: number; seats_sold: number; gross_minor: string; fee_minor: string; net_minor: string };
type EventSales = Revenue & { id: string; title: string; starts_at: string; currency: string; capacity: number; interested: number };
type Count = { key: number; count: number };
type Tenant = { time_zone: string; fee_bps: number; totals: (Revenue & { currency: string })[]; events: EventSales[]; event_count: number };
type Event = {
  time_zone: string; event: EventSales; sales_open_at: string;
  funnel: { interested: number; holds: number; confirmed: number; expired: number; cancelled_holds: number; cancelled_bookings: number };
  daily: { day: string; seats_sold: number; net_minor: string }[];
  sections: { section: string; capacity: number; seats_sold: number; gross_minor: string }[];
  hours: Count[]; opening: Count[]; first_booking_seconds: number | null; sold_out_seconds: number | null;
};
type Props = { tenantID: string; userID: string; heading: RefObject<HTMLHeadingElement | null>; onSessionExpired: () => void };

// Formats minor units exactly: Intl formats a decimal string without rounding it through a float.
function money(minor: string, currency: string) {
  const negative = minor.startsWith('-');
  const digits = (negative ? minor.slice(1) : minor).padStart(3, '0');
  const decimal = `${negative ? '-' : ''}${digits.slice(0, -2)}.${digits.slice(-2)}`;
  return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(decimal as unknown as number);
}
// A time zone the browser cannot use falls back to UTC instead of breaking the page.
function usableZone(zone: string) {
  try { new Intl.DateTimeFormat(undefined, { timeZone: zone }); return zone; } catch { return 'UTC'; }
}
const percent = (part: number, whole: number) => whole ? `${Math.round(part / whole * 100)}%` : '—';
const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;
function duration(seconds: number) {
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return plural(minutes, 'minute');
  const hours = Math.round(minutes / 60);
  return hours < 48 ? plural(hours, 'hour') : plural(Math.round(hours / 24), 'day');
}

// Proportions as labelled bars; every value is also written out as text.
// Muted rows are outcomes that did not become sales, so they never read like revenue.
function Bars({ label, rows }: { label: string; rows: { name: string; value: number; max: number; text: string; muted?: boolean }[] }) {
  return <ul className="insight-bars" aria-label={label}>{rows.map(row => <li key={row.name} data-muted={row.muted || undefined}>
    <span>{row.name}</span><span className="insight-value">{row.text}</span>
    <span className="insight-track" aria-hidden="true"><span style={{ width: `${row.max ? Math.min(100, row.value / row.max * 100) : 0}%` }} /></span>
  </li>)}</ul>;
}

// A column chart over time. The picture is decorative; the values are in a table anyone can open.
function Columns({ label, columns }: { label: string; columns: { key: string; label: string; value: number; text: string; listed?: boolean }[] }) {
  const max = Math.max(...columns.map(column => column.value), 1);
  const width = 100 / columns.length;
  const peak = columns.reduce((best, column) => column.value > best.value ? column : best, columns[0]);
  return <figure className="insight-columns">
    {/* The tallest column stands in for a y-axis: every other column is a share of it. */}
    {peak && peak.value > 0 && <p className="insight-peak">Highest: {peak.label} · {peak.text}</p>}
    <svg viewBox="0 0 100 40" preserveAspectRatio="none" aria-hidden="true">
      {columns.map((column, index) => { const height = column.value / max * 38;
        return <rect key={column.key} x={index * width + width * 0.15} width={width * 0.7} y={40 - height} height={height}><title>{`${column.label}: ${column.text}`}</title></rect>; })}
    </svg>
    <figcaption><span>{columns[0]?.label}</span><span>{columns[columns.length - 1]?.label}</span></figcaption>
    <details className="insight-data"><summary>{label}: show values</summary>
      <table><tbody>{columns.filter(column => column.listed || column.value).map(column => <tr key={column.key}><th scope="row">{column.label}</th><td>{column.text}</td></tr>)}</tbody></table>
    </details>
  </figure>;
}

// Days without sales are missing from the API; fill them so the gaps show.
function everyDay(daily: Event['daily']) {
  if (!daily.length) return [];
  const known = new Map(daily.map(day => [day.day.slice(0, 10), day]));
  const days: (Event['daily'][number] & { filled?: boolean })[] = [];
  const last = Date.parse(`${daily[daily.length - 1].day.slice(0, 10)}T00:00:00Z`);
  for (let time = Date.parse(`${daily[0].day.slice(0, 10)}T00:00:00Z`); time <= last && days.length < 366; time += 86_400_000) {
    const key = new Date(time).toISOString().slice(0, 10);
    days.push(known.get(key) ?? { day: key, seats_sold: 0, net_minor: '0', filled: true });
  }
  return days;
}

function hourColumns(counts: Count[], label: (key: number) => string) {
  return Array.from({ length: 24 }, (_, key) => {
    const value = counts.find(count => count.key === key)?.count ?? 0;
    return { key: String(key), label: label(key), value, text: plural(value, 'booking') };
  });
}

function Tiles({ items }: { items: [string, string][] }) {
  return <dl className="organizer-details insight-tiles">{items.map(([term, value]) => <div key={term}><dt>{term}</dt><dd>{value}</dd></div>)}</dl>;
}

export function TenantInsights({ tenantID, userID, heading, onSessionExpired, tenantName }: Props & { tenantName: string }) {
  const { data, error, forbidden, retry } = usePrivateResource<Tenant>(`/organizer/tenants/${tenantID}/insights`, userID, onSessionExpired);
  useEffect(() => { if (data) heading.current?.focus(); }, [data, heading]);
  const zone = data ? usableZone(data.time_zone) : 'UTC';
  const day = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeZone: zone });
  return <>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Insights</h1>
      <p>{data ? `${tenantName} · Platform fee ${data.fee_bps / 100}% · Dates in ${zone}` : `${tenantName} · Revenue across published events.`}</p></div>
    {error && <ErrorMessage message={error} retry={forbidden ? undefined : retry} />}
    {!data && !error && <p className="loading" role="status">Loading insights…</p>}
    {data && (data.totals.length === 0 ? <p className="organizer-empty">No bookings yet. Revenue appears after the first confirmed booking.</p>
      : data.totals.map(total => <section key={total.currency} className="insight-section" aria-label={`${total.currency} revenue`}>
        {data.totals.length > 1 && <h2>{total.currency}</h2>}
        <Tiles items={[['Net revenue', money(total.net_minor, total.currency)], ['Gross', money(total.gross_minor, total.currency)],
          ['Platform fee', money(total.fee_minor, total.currency)], ['Bookings', String(total.bookings)], ['Seats sold', String(total.seats_sold)]]} />
      </section>))}
    {data && data.events.length > 0 && <section className="insight-section"><h2>Events</h2>
      {data.event_count > data.events.length && <p className="insight-note">Showing the {data.events.length} latest of {data.event_count} published events. Totals above include them all.</p>}
      <ul className="insight-events">{data.events.map(event => <li key={event.id}>
        <div><a href={`#organizer/${tenantID}/${event.id}/insights`}>{event.title}</a><p>{day.format(new Date(event.starts_at))} · {plural(event.interested, 'interested customer')}</p></div>
        <Bars label={`${event.title} sell-through`} rows={[{ name: 'Sold', value: event.seats_sold, max: event.capacity, text: `${event.seats_sold} of ${event.capacity} · ${percent(event.seats_sold, event.capacity)}` }]} />
        <p className="price">{event.bookings ? money(event.net_minor, event.currency) : '—'}<small>net</small></p>
      </li>)}</ul>
    </section>}
  </>;
}

export function EventInsights({ tenantID, eventID, userID, heading, onSessionExpired }: Props & { eventID: string }) {
  const { data, error, forbidden, retry } = usePrivateResource<Event>(`/organizer/tenants/${tenantID}/events/${eventID}/insights`, userID, onSessionExpired);
  useEffect(() => { if (data) heading.current?.focus(); }, [data, heading]);
  const event = data?.event;
  const zone = data ? usableZone(data.time_zone) : 'UTC';
  return <>
    <div className="page-heading"><h1 ref={heading} tabIndex={-1}>Insights</h1>
      <p>{event ? `${event.title} · Times in ${zone}` : 'Sales, holds, and timing for this event.'}</p></div>
    {error && <ErrorMessage message={error} retry={forbidden ? undefined : retry} />}
    {!data && !error && <p className="loading" role="status">Loading insights…</p>}
    {data && event && (data.funnel.holds === 0 && data.funnel.interested === 0
      ? <p className="organizer-empty">No activity yet. Numbers appear after the first hold or interested customer.</p> : <>
      <Tiles items={[
        ['Net revenue', event.bookings ? money(event.net_minor, event.currency) : '—'],
        ['Gross', event.bookings ? money(event.gross_minor, event.currency) : '—'],
        ['Platform fee', event.bookings ? money(event.fee_minor, event.currency) : '—'],
        ['Sell-through', `${percent(event.seats_sold, event.capacity)} · ${event.seats_sold} of ${event.capacity} unblocked`],
        ['Seats per booking', event.bookings ? (event.seats_sold / event.bookings).toFixed(1) : '—'],
        ['Hold to booking', percent(data.funnel.confirmed, data.funnel.holds)],
      ]} />
      <section className="insight-section"><h2>Funnel</h2>
        <Bars label="Funnel" rows={([['Interested', data.funnel.interested], ['Holds', data.funnel.holds], ['Booked', data.funnel.confirmed]] as const)
          .map(([name, value]) => ({ name, value, max: Math.max(data.funnel.interested, data.funnel.holds), text: String(value) }))} />
      </section>
      {data.funnel.holds > 0 && <section className="insight-section"><h2>How holds ended</h2>
        <Bars label="How holds ended" rows={([['Booked', data.funnel.confirmed, false], ['Expired', data.funnel.expired, true],
          ['Cancelled by customer', data.funnel.cancelled_holds, true],
          ['Still open', data.funnel.holds - data.funnel.confirmed - data.funnel.expired - data.funnel.cancelled_holds, false]] as const)
          .filter(([name, value]) => name !== 'Still open' || value > 0)
          .map(([name, value, muted]) => ({ name, value, max: data.funnel.holds, text: `${value} · ${percent(value, data.funnel.holds)}`, muted }))} />
        {data.funnel.cancelled_bookings > 0 && <p className="insight-note">{data.funnel.cancelled_bookings} of {plural(data.funnel.confirmed, 'booking')} {data.funnel.cancelled_bookings === 1 ? 'was' : 'were'} cancelled later.</p>}
      </section>}
      {data.daily.length > 0 && <section className="insight-section"><h2>Net revenue by day</h2>
        <Columns label="Net revenue by day" columns={everyDay(data.daily).map(day => ({ key: day.day, label: new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeZone: 'UTC' }).format(new Date(day.day)),
          value: Number(day.net_minor), text: `${money(day.net_minor, event.currency)}, ${plural(day.seats_sold, 'seat')}`, listed: !('filled' in day) }))} />
      </section>}
      <section className="insight-section"><h2>Sections</h2>
        <Bars label="Seats sold by section" rows={data.sections.map(section => ({ name: section.section, value: section.seats_sold, max: section.capacity,
          text: `${section.seats_sold} of ${section.capacity} · ${money(section.gross_minor, event.currency)} gross` }))} />
      </section>
      {event.bookings > 0 && <>
        <section className="insight-section"><h2>Busiest hours</h2>
          <Columns label="Bookings by hour of day" columns={hourColumns(data.hours, hour => `${String(hour).padStart(2, '0')}:00`)} />
        </section>
        <section className="insight-section"><h2>First 24 hours of sales</h2>
          {data.opening.length ? <Columns label="Bookings by hour after sales opened" columns={hourColumns(data.opening, hour => `Hour ${hour + 1}`)} />
            : <p className="organizer-empty">No bookings in the first 24 hours after sales opened.</p>}
        </section>
      </>}
      <section className="insight-section"><h2>Timing</h2>
        <Tiles items={[
          ['Sales opened', new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: zone }).format(new Date(data.sales_open_at))],
          ['First booking', data.first_booking_seconds !== null ? `${duration(data.first_booking_seconds)} after opening`
            : event.bookings ? 'None since the current opening' : 'No bookings yet'],
          ['Sold out', data.sold_out_seconds !== null ? `${duration(data.sold_out_seconds)} after opening`
            : event.seats_sold === event.capacity && event.capacity > 0 ? 'Sold out, time unknown' : 'Not sold out'],
        ]} />
      </section>
    </>)}
  </>;
}
