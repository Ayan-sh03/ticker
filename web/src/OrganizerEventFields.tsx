import type { DraftEventInput, OrganizerEvent } from './api';
import { OrganizerDateTime } from './OrganizerDateTime';
import { OrganizerSelect } from './OrganizerSelect';

export type Fields = {
  title: string; description: string; venue: string; amount: string; currency: string;
  sales: string; starts: string; rows: string; columns: string;
};
export function localDateTime(value: string) {
  const date = new Date(value);
  const pad = (number: number) => String(number).padStart(2, '0');
  return `${String(date.getFullYear()).padStart(4, '0')}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function fieldsForEvent(event: OrganizerEvent): Fields {
  return { title: event.title, description: event.description, venue: event.venue_name,
    amount: `${Math.floor(event.price_minor / 100)}.${String(event.price_minor % 100).padStart(2, '0')}`,
    currency: event.currency, sales: localDateTime(event.sales_open_at), starts: localDateTime(event.starts_at),
    rows: String(event.layout_rows), columns: String(event.layout_columns) };
}

function localTime(value: string): Date {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value);
  if (!match) throw new Error('Enter a valid date and time for sales and the event.');
  const [year, month, day, hour, minute] = match.slice(1).map(Number);
  const result = new Date(0);
  result.setFullYear(year, month - 1, day);
  result.setHours(hour, minute, 0, 0);
  if (year < 1 || year > 9999 || result.getFullYear() !== year || result.getMonth() !== month - 1 || result.getDate() !== day
    || result.getHours() !== hour || result.getMinutes() !== minute || result.getUTCFullYear() < 1 || result.getUTCFullYear() > 9999) {
    throw new Error('That date or time does not exist in your local timezone. Choose another time.');
  }
  return result;
}

export function draftPayload(fields: Fields, original?: OrganizerEvent): DraftEventInput {
  const title = fields.title.trim(), description = fields.description.trim(), venue = fields.venue.trim();
  if (!title || !venue || [...title].length > 200 || [...venue].length > 200 || [...description].length > 5000 || `${title}${description}${venue}`.includes('\0')) {
    throw new Error('Enter a title and venue up to 200 characters, and a description up to 5,000 characters.');
  }
  const amount = fields.amount.trim();
  if (!/^\d+(?:\.\d{1,2})?$/.test(amount)) throw new Error('Enter a nonnegative price with up to two decimal places, such as 250.00.');
  const [whole, fraction = ''] = amount.split('.');
  const minor = BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'));
  if (minor > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('The price is too large. Enter a smaller amount.');
  if (!['INR', 'USD', 'EUR', 'GBP'].includes(fields.currency) && fields.currency !== original?.currency) throw new Error('Choose a supported currency.');
  const startsUnchanged = original && fields.starts === localDateTime(original.starts_at);
  const salesUnchanged = original && fields.sales === localDateTime(original.sales_open_at);
  const starts = startsUnchanged ? new Date(original.starts_at) : localTime(fields.starts);
  const sales = salesUnchanged ? new Date(original.sales_open_at) : localTime(fields.sales);
  if (!startsUnchanged && starts.getTime() <= Date.now()) throw new Error('The event must start in the future.');
  if (sales.getTime() >= starts.getTime()) throw new Error('Sales must open before the event starts.');
  const rows = Number(fields.rows), columns = Number(fields.columns);
  if (!/^\d+$/.test(fields.rows) || !/^\d+$/.test(fields.columns) || rows < 1 || rows > 100 || columns < 1 || columns > 100) {
    throw new Error('Enter whole numbers from 1 to 100 for rows and seats per row.');
  }
  return { title, description, venue_name: venue, price_minor: Number(minor), currency: fields.currency,
    sales_open_at: salesUnchanged ? original.sales_open_at : sales.toISOString(), starts_at: startsUnchanged ? original.starts_at : starts.toISOString(), layout_rows: rows, layout_columns: columns };
}

export function OrganizerEventFields({ fields, update, disabled, priceLayoutEditable = true, originalCurrency, mode = 'create' }: {
  fields: Fields; update: (field: keyof Fields, value: string) => void; disabled: boolean;
  priceLayoutEditable?: boolean; originalCurrency?: string; mode?: 'create' | 'edit';
}) {
  const id = mode === 'create' ? 'draft' : 'edit';
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const seatCount = Number(fields.rows) * Number(fields.columns);
  const currencies = ['INR', 'USD', 'EUR', 'GBP'];
  if (mode === 'edit' && originalCurrency && !currencies.includes(originalCurrency)) currencies.push(originalCurrency);
  return <>
      <fieldset className="organizer-section" disabled={disabled}><legend>Event details</legend>
        <div className="organizer-grid">
          <div className="organizer-field organizer-field-wide"><label htmlFor={`${id}-title`}>Event title</label><input id={`${id}-title`} value={fields.title} onChange={event => update('title', event.target.value)} maxLength={200} required /></div>
          <div className="organizer-field organizer-field-wide"><label htmlFor={`${id}-venue`}>Venue</label><input id={`${id}-venue`} value={fields.venue} onChange={event => update('venue', event.target.value)} maxLength={200} required /></div>
          <div className="organizer-field organizer-field-wide"><label htmlFor={`${id}-description`}>Description <span className="organizer-note">(optional)</span></label><textarea id={`${id}-description`} value={fields.description} onChange={event => update('description', event.target.value)} maxLength={5000} rows={4} /></div>
        </div>
      </fieldset>
      <fieldset className="organizer-section" disabled={disabled}><legend>Price and schedule</legend>
        <div className="organizer-grid">
          <div className="organizer-field"><label htmlFor={`${id}-amount`}>Price per seat</label><input disabled={!priceLayoutEditable} id={`${id}-amount`} inputMode="decimal" value={fields.amount} onChange={event => update('amount', event.target.value)} pattern="[0-9]+(\.[0-9]{1,2})?" maxLength={20} placeholder="250.00" aria-describedby={`${id}-price-help`} required /><p id={`${id}-price-help`} className="organizer-note">Use up to two decimal places. Enter 0 for a free event.</p></div>
          <div className="organizer-field"><label htmlFor={`${id}-currency`}>Currency</label><OrganizerSelect id={`${id}-currency`} label="Currency" value={fields.currency} onChange={value => update('currency', value)} disabled={disabled || !priceLayoutEditable} options={currencies.map(value => ({ value, label: value }))} /></div>
          <div className="organizer-field"><label htmlFor={`${id}-sales`}>Sales open</label><OrganizerDateTime id={`${id}-sales`} label="Sales open" value={fields.sales} onChange={value => update('sales', value)} disabled={disabled} describedBy={`${id}-time-help`} /></div>
          <div className="organizer-field"><label htmlFor={`${id}-starts`}>Event starts</label><OrganizerDateTime id={`${id}-starts`} label="Event starts" value={fields.starts} onChange={value => update('starts', value)} disabled={disabled} describedBy={`${id}-time-help`} /></div>
          <p id={`${id}-time-help`} className="organizer-note organizer-field-wide">Enter times in {timezone}. Sales must open before the event starts.</p>
        </div>
      </fieldset>
      <fieldset className="organizer-section" disabled={disabled || !priceLayoutEditable}><legend>Seat layout</legend>
        <div className="organizer-grid">
          <div className="organizer-field"><label htmlFor={`${id}-rows`}>Rows</label><input id={`${id}-rows`} type="number" value={fields.rows} onChange={event => update('rows', event.target.value)} min={1} max={100} step={1} aria-describedby={`${id}-seat-help`} required /></div>
          <div className="organizer-field"><label htmlFor={`${id}-columns`}>Seats per row</label><input id={`${id}-columns`} type="number" value={fields.columns} onChange={event => update('columns', event.target.value)} min={1} max={100} step={1} aria-describedby={`${id}-seat-help`} required /></div>
          <p id={`${id}-seat-help`} className="organizer-note organizer-field-wide">{Number.isInteger(seatCount) && seatCount > 0 && Number(fields.rows) <= 100 && Number(fields.columns) <= 100 ? `${seatCount.toLocaleString()} seats · ` : ''}{mode === 'create' ? 'A rectangular layout in one section. Seats are created when you publish.' : 'A rectangular layout in one section.'}</p>
        </div>
      </fieldset>
  </>;
}
