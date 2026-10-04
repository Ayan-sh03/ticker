/* Hallmark · component: date-time picker · genre: modern-minimal · theme: Coral */
import { useEffect, useRef, useState, type KeyboardEvent } from 'react';

const pad = (value: number) => String(value).padStart(2, '0');
function dayDate(year: number, month: number, day: number) {
  const result = new Date(0);
  result.setFullYear(year, month, day);
  result.setHours(12, 0, 0, 0);
  return result;
}
const dayKey = (date: Date) => `${String(date.getFullYear()).padStart(4, '0')}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
function selectedDate(value: string) {
  const match = /^(\d{4})-(\d{2})-(\d{2})T\d{2}:\d{2}$/.exec(value);
  if (!match) return null;
  const [year, month, day] = match.slice(1).map(Number);
  const date = dayDate(year, month - 1, day);
  return year >= 1 && year <= 9999 && dayKey(date) === value.slice(0, 10) ? date : null;
}

export function OrganizerDateTime({ id, label, value, onChange, disabled, describedBy }: {
  id: string; label: string; value: string; onChange: (value: string) => void;
  disabled: boolean; describedBy: string;
}) {
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [focused, setFocused] = useState(() => selectedDate(value) ?? dayDate(new Date().getFullYear(), new Date().getMonth(), new Date().getDate()));
  const [time, setTime] = useState(value.slice(11) || '19:00');
  const selected = selectedDate(value);
  const first = dayDate(focused.getFullYear(), focused.getMonth(), 1);
  const offset = (first.getDay() + 6) % 7;
  const days = Array.from({ length: 42 }, (_, index) => dayDate(first.getFullYear(), first.getMonth(), index - offset + 1));
  const monthName = new Intl.DateTimeFormat(undefined, { month: 'long' }).format(first);
  const today = dayKey(new Date());

  function close(restore = false) { setOpen(false); if (restore) trigger.current?.focus(); }
  function show() {
    if (open) { close(); return; }
    setFocused(selected ?? dayDate(new Date().getFullYear(), new Date().getMonth(), new Date().getDate()));
    setTime(value.slice(11) || '19:00');
    setOpen(true);
  }
  useEffect(() => {
    if (!open) return;
    if (panel.current?.contains(document.activeElement) && document.activeElement?.tagName === 'INPUT') return;
    panel.current?.querySelector<HTMLButtonElement>('[data-focused="true"]')?.focus({ preventScroll: true });
  }, [open, focused]);
  useEffect(() => { if (open) panel.current?.scrollIntoView({ block: 'nearest' }); }, [open]);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false); };
    document.addEventListener('pointerdown', dismiss);
    return () => document.removeEventListener('pointerdown', dismiss);
  }, [open]);
  useEffect(() => { if (disabled) setOpen(false); }, [disabled]);

  function choose(date: Date) { setFocused(date); onChange(`${dayKey(date)}T${time}`); }
  function moveMonth(delta: number) {
    const date = dayDate(focused.getFullYear(), focused.getMonth() + delta, 1);
    if (date.getFullYear() >= 1 && date.getFullYear() <= 9999) setFocused(date);
  }
  function navigate(event: KeyboardEvent<HTMLButtonElement>, date: Date) {
    const shifts: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7,
      Home: -((date.getDay() + 6) % 7), End: 6 - ((date.getDay() + 6) % 7) };
    if (event.key in shifts) {
      event.preventDefault();
      const next = dayDate(date.getFullYear(), date.getMonth(), date.getDate() + shifts[event.key]);
      if (next.getFullYear() >= 1 && next.getFullYear() <= 9999) setFocused(next);
    } else if (event.key === 'PageUp' || event.key === 'PageDown') {
      event.preventDefault(); moveMonth((event.key === 'PageUp' ? -1 : 1) * (event.shiftKey ? 12 : 1));
    }
  }
  function changeTime(part: number, raw: string) {
    if (!/^\d{1,2}$/.test(raw) || Number(raw) > (part === 0 ? 23 : 59)) return;
    const parts = time.split(':'); parts[part] = pad(Number(raw));
    const next = parts.join(':'); setTime(next);
    if (selected) onChange(`${dayKey(selected)}T${next}`);
  }

  return <div className="organizer-date" ref={root}
    onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) close(); }}
    onKeyDown={event => { if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); close(true); } }}>
    <div className="organizer-date-control">
      <input id={id} value={value.replace('T', ' ')} onChange={event => onChange(event.target.value.replace(' ', 'T'))}
        placeholder="YYYY-MM-DD HH:mm" pattern="[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}"
        maxLength={16} required disabled={disabled} aria-describedby={describedBy} autoComplete="off" />
      <button ref={trigger} type="button" className="organizer-calendar-trigger" aria-label={`Choose ${label.toLowerCase()} date and time`}
        aria-expanded={open} aria-controls={`${id}-calendar`} aria-haspopup="dialog" disabled={disabled} onClick={show}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><rect x="4" y="5" width="16" height="16" rx="2" /><path d="M8 3v4m8-4v4M4 11h16" /></svg>
      </button>
    </div>
    {open && <div ref={panel} id={`${id}-calendar`} className="organizer-calendar" role="dialog" aria-label={`${label} date and time`}>
      <div className="organizer-calendar-heading">
        <button type="button" className="organizer-calendar-nav" aria-label="Previous month" disabled={first.getFullYear() === 1 && first.getMonth() === 0} onClick={() => moveMonth(-1)}>←</button>
        <p aria-live="polite">{monthName} {first.getFullYear()}</p>
        <button type="button" className="organizer-calendar-nav" aria-label="Next month" disabled={first.getFullYear() === 9999 && first.getMonth() === 11} onClick={() => moveMonth(1)}>→</button>
      </div>
      <div className="organizer-calendar-week" aria-hidden="true">{['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'].map(day => <span key={day}>{day}</span>)}</div>
      <div className="organizer-calendar-days" role="group" aria-label={`${monthName} ${first.getFullYear()}`}>
        {days.map(date => { const key = dayKey(date); return <button type="button" key={key}
          className="organizer-calendar-day" data-outside={date.getMonth() !== first.getMonth()} data-today={key === today}
          data-focused={key === dayKey(focused)} aria-pressed={key === (selected && dayKey(selected))}
          aria-current={key === today ? 'date' : undefined} aria-label={new Intl.DateTimeFormat(undefined, { dateStyle: 'full' }).format(date)}
          disabled={date.getFullYear() < 1 || date.getFullYear() > 9999} tabIndex={key === dayKey(focused) ? 0 : -1}
          onKeyDown={event => navigate(event, date)} onClick={() => choose(date)}>{date.getDate()}</button>; })}
      </div>
      <div className="organizer-calendar-settings">
        <label>Year<input type="number" min={1} max={9999} value={first.getFullYear()} onChange={event => {
          const year = Number(event.target.value); if (Number.isInteger(year) && year >= 1 && year <= 9999) setFocused(dayDate(year, first.getMonth(), 1));
        }} /></label>
        <div className="organizer-calendar-time"><label>Hour<input aria-label={`${label} hour`} type="number" min={0} max={23} value={time.split(':')[0]} onChange={event => changeTime(0, event.target.value)} /></label>
          <span aria-hidden="true">:</span><label>Minute<input aria-label={`${label} minute`} type="number" min={0} max={59} value={time.split(':')[1]} onChange={event => changeTime(1, event.target.value)} /></label></div>
      </div>
      <p className="organizer-calendar-hint">24-hour time · Choose a day to apply.</p>
      <div className="organizer-calendar-footer"><button type="button" onClick={() => choose(dayDate(new Date().getFullYear(), new Date().getMonth(), new Date().getDate()))}>Today</button>
        <button type="button" onClick={() => { onChange(''); close(true); }}>Clear</button><button type="button" onClick={() => close(true)}>Done</button></div>
    </div>}
  </div>;
}
