import { memo, useEffect, useId, useMemo, useRef, useState, type CSSProperties, type FocusEvent, type KeyboardEvent, type MouseEvent, type PointerEvent, type RefObject } from 'react';
import type { Seat } from './api';

// Tones mirror the legend. "selected" is the one coral state: seats that are yours.
export type SeatTone = 'available' | 'selected' | 'held' | 'booked' | 'blocked';
export interface SeatLook { tone: SeatTone; enabled: boolean; label: string }
export interface RowAction { label: string; disabled: boolean; run: () => void }

export const seatTones: Record<Seat['status'], SeatTone> = { AVAILABLE: 'available', HELD: 'held', SOLD: 'booked', BLOCKED: 'blocked' };

// The aisle and the bow are drawn only; they never add or remove seats.
const aisleFrom = 8;
const curveFrom = 6;
const maxDepthPx = 22;
// The aisle is about a third of a seat pitch wide; a pitch is about 50px.
const aisleSeats = 0.35;
const pitchPx = 50;

interface Line { section: number; name: string; number: number; seats: Seat[] }

// Seats arrive ordered by section, row and seat, so one pass builds the rows.
function lines(seats: Seat[]) {
  const result: Line[] = [];
  let names = 0;
  for (const seat of seats) {
    const last = result[result.length - 1];
    if (last && last.name === seat.section && last.number === seat.row_number) { last.seats.push(seat); continue; }
    if (!last || last.name !== seat.section) names++;
    result.push({ section: names - 1, name: seat.section, number: seat.row_number, seats: [seat] });
  }
  return result;
}

const depth = (length: number) => length < curveFrom ? 0 : Math.min(maxDepthPx, 2.25 * length);

// One parabola spans the whole row, aisle included, so both halves share a
// curve. Each seat drops by `lift` (0 at the ends, 1 in the middle) and turns
// to the curve's slope, so the row reads as an arc rather than steps.
function arc(index: number, length: number, split: number) {
  if (length < curveFrom) return { lift: 0, tilt: 0 };
  const at = (i: number) => i + (i >= split ? aisleSeats : 0);
  const half = at(length - 1) / 2;
  const t = (at(index) - half) / half;
  const tilt = Math.atan(-2 * depth(length) * t / (half * pitchPx)) * 180 / Math.PI;
  return { lift: Math.round((1 - t * t) * 1000) / 1000, tilt: Math.round(tilt * 100) / 100 };
}

const SeatCell = memo(function SeatCell({ seat, tone, enabled, label, tabbable, swept, lift, tilt }: {
  seat: Seat; tone: SeatTone; enabled: boolean; label: string; tabbable: boolean; swept: boolean; lift: number; tilt: number;
}) {
  return <button type="button" className="venue-seat" data-seat={seat.id} data-tone={tone} data-swept={swept || undefined}
    aria-disabled={!enabled || undefined} aria-pressed={tone === 'selected'} aria-label={label} tabIndex={tabbable ? 0 : -1}
    style={lift || tilt ? { '--bow': lift, '--tilt': tilt } as CSSProperties : undefined}><span>{seat.seat_number}</span></button>;
});

// Shows where the frame sits in a room wider than the screen, and where your seats are.
function Overview({ frame, marks }: { frame: RefObject<HTMLDivElement | null>; marks: number[] }) {
  const [view, setView] = useState<{ left: number; width: number } | null>(null);
  useEffect(() => {
    const element = frame.current;
    if (!element) return;
    const update = () => setView(element.scrollWidth > element.clientWidth + 1
      ? { left: element.scrollLeft / element.scrollWidth, width: element.clientWidth / element.scrollWidth } : null);
    update();
    element.addEventListener('scroll', update, { passive: true });
    const observer = new ResizeObserver(update);
    observer.observe(element);
    if (element.firstElementChild) observer.observe(element.firstElementChild);
    return () => { element.removeEventListener('scroll', update); observer.disconnect(); };
  }, [frame]);
  if (!view) return null;
  const jump = (event: PointerEvent<HTMLDivElement>) => {
    const element = frame.current;
    if (!element) return;
    const box = event.currentTarget.getBoundingClientRect();
    element.scrollLeft = (event.clientX - box.left) / box.width * element.scrollWidth - element.clientWidth / 2;
  };
  return <div className="venue-overview" aria-hidden="true"
    onPointerDown={event => { event.currentTarget.setPointerCapture(event.pointerId); jump(event); }}
    onPointerMove={event => { if (event.buttons) jump(event); }}>
    <span className="venue-overview-view" style={{ left: `${view.left * 100}%`, width: `${view.width * 100}%` }} />
    {marks.map((mark, index) => <span key={index} className="venue-overview-mark" style={{ left: `${mark * 100}%` }} />)}
  </div>;
}

// One map for customers and organizers. It is a single tab stop; arrow keys move
// between seats. With onRange, shift-click or a mouse drag selects a block of
// seats in one section, and rowAction turns each row number into a button.
export const SeatMap = memo(function SeatMap({ seats, look, onToggle, onRange, rowAction, sectionHeading, hint, hintVisible }: {
  seats: Seat[]; look: (seat: Seat) => SeatLook; onToggle: (seatID: string) => void;
  onRange?: (seatIDs: string[], select: boolean) => void; rowAction?: (seats: Seat[]) => RowAction | null;
  sectionHeading: 'h2' | 'h3'; hint: string; hintVisible?: boolean;
}) {
  const rows = useMemo(() => lines(seats), [seats]);
  const positions = useMemo(() => {
    const result = new Map<string, { line: number; column: number }>();
    rows.forEach((row, line) => row.seats.forEach((seat, column) => result.set(seat.id, { line, column })));
    return result;
  }, [rows]);
  const looks = useMemo(() => new Map(seats.map(seat => [seat.id, look(seat)])), [seats, look]);
  const root = useRef<HTMLDivElement>(null);
  const frame = useRef<HTMLDivElement>(null);
  const active = useRef('');
  const anchor = useRef('');
  const drag = useRef<{ from: string; select: boolean; moved: boolean } | null>(null);
  const skipClick = useRef(false);
  // The swept block is mirrored in a ref so the drag can end outside React's render.
  const [swept, setSwept] = useState<Set<string>>(() => new Set());
  const sweptIDs = useRef<string[]>([]);
  const hintID = useId();

  if (!positions.has(active.current)) active.current = seats.find(seat => looks.get(seat.id)?.tone === 'selected')?.id ?? seats[0]?.id ?? '';
  const sections = rows.reduce((count, row) => Math.max(count, row.section + 1), 0);

  // Seats in the rectangle between two seats of one section that can change.
  function block(from: string, to: string) {
    const a = positions.get(from), b = positions.get(to);
    if (!a || !b || rows[a.line].section !== rows[b.line].section) return [to];
    const ids: string[] = [];
    for (let line = Math.min(a.line, b.line); line <= Math.max(a.line, b.line); line++)
      for (let column = Math.min(a.column, b.column); column <= Math.max(a.column, b.column); column++) {
        const seat = rows[line].seats[column];
        if (seat && looks.get(seat.id)?.enabled) ids.push(seat.id);
      }
    return ids;
  }

  useEffect(() => {
    const end = () => {
      const current = drag.current;
      drag.current = null;
      if (!current?.moved) return;
      if (sweptIDs.current.length) onRange?.(sweptIDs.current, current.select);
      sweptIDs.current = [];
      setSwept(new Set());
      skipClick.current = true;
    };
    // A drag that is cancelled, released outside the window, or interrupted by
    // switching windows commits what it swept, so the next drag starts clean.
    const events = ['pointerup', 'pointercancel', 'blur'] as const;
    for (const name of events) window.addEventListener(name, end);
    return () => { for (const name of events) window.removeEventListener(name, end); };
  }, [onRange]);

  function seatOf(target: EventTarget) {
    const id = (target as HTMLElement).closest<HTMLElement>('[data-seat]')?.dataset.seat;
    return id && positions.has(id) ? id : '';
  }

  function click(event: MouseEvent<HTMLDivElement>) {
    if (skipClick.current) { skipClick.current = false; return; }
    const id = seatOf(event.target);
    if (!id || !looks.get(id)?.enabled) return;
    if (event.shiftKey && onRange && anchor.current && positions.has(anchor.current)) onRange(block(anchor.current, id), looks.get(id)?.tone !== 'selected');
    else onToggle(id);
    anchor.current = id;
  }

  function pointerDown(event: PointerEvent<HTMLDivElement>) {
    skipClick.current = false;
    if (!onRange || event.pointerType !== 'mouse' || event.button !== 0 || event.shiftKey) return;
    const id = seatOf(event.target);
    if (id && looks.get(id)?.enabled) drag.current = { from: id, select: looks.get(id)?.tone !== 'selected', moved: false };
  }

  function pointerOver(event: PointerEvent<HTMLDivElement>) {
    const current = drag.current;
    const id = seatOf(event.target);
    if (!current || !id || (!current.moved && id === current.from)) return;
    current.moved = true;
    anchor.current = current.from;
    sweptIDs.current = block(current.from, id);
    setSwept(new Set(sweptIDs.current));
  }

  function focusAt(line: number, column: number) {
    const row = rows[line];
    if (!row) return;
    const selector = column < 0 ? `[data-row="${line}"]` : `[data-seat="${row.seats[Math.min(column, row.seats.length - 1)].id}"]`;
    root.current?.querySelector<HTMLElement>(selector)?.focus();
  }

  function keyDown(event: KeyboardEvent<HTMLDivElement>) {
    const target = event.target as HTMLElement;
    const id = target.dataset.seat;
    const line = id ? positions.get(id)?.line : target.dataset.row !== undefined ? Number(target.dataset.row) : undefined;
    if (line === undefined) return;
    const column = id ? positions.get(id)!.column : -1;
    const last = rows[line].seats.length - 1;
    const labelled = (index: number) => !!root.current?.querySelector(`[data-row="${index}"]:not(:disabled)`);
    const vertical = (next: number) => focusAt(next, column < 0 && !labelled(next) ? 0 : column);
    const moves: Record<string, () => void> = {
      ArrowRight: () => focusAt(line, Math.min(column + 1, last)),
      ArrowLeft: () => focusAt(line, column > 0 ? column - 1 : labelled(line) ? -1 : 0),
      ArrowDown: () => vertical(Math.min(line + 1, rows.length - 1)),
      ArrowUp: () => vertical(Math.max(line - 1, 0)),
      Home: () => focusAt(line, 0),
      End: () => focusAt(line, last),
    };
    const move = moves[event.key];
    if (!move) return;
    event.preventDefault();
    move();
  }

  // Keep a single tab stop on the last focused seat without re-rendering the map.
  function focus(event: FocusEvent<HTMLDivElement>) {
    const id = seatOf(event.target);
    if (!id || id === active.current) return;
    root.current?.querySelector<HTMLElement>(`[data-seat="${active.current}"]`)?.setAttribute('tabindex', '-1');
    (event.target as HTMLElement).setAttribute('tabindex', '0');
    active.current = id;
  }

  const marks = [...new Set(seats.flatMap(seat => {
    if (looks.get(seat.id)?.tone !== 'selected') return [];
    const position = positions.get(seat.id)!;
    return [(position.column + 0.5) / rows[position.line].seats.length];
  }))];
  const Heading = sectionHeading;

  return <div className="venue" ref={root}>
    <p id={hintID} className={hintVisible ? 'venue-hint' : 'visually-hidden'}>{hint}</p>
    <Overview frame={frame} marks={marks} />
    <div className="venue-frame" ref={frame}>
      <div className="venue-floor" role="group" aria-label="Seat map" aria-describedby={hintID}
        onClick={click} onPointerDown={pointerDown} onPointerOver={pointerOver} onKeyDown={keyDown} onFocus={focus}>
        <div className="venue-stage">Stage</div>
        {Array.from({ length: sections }, (_, section) => {
          const sectionRows = rows.filter(row => row.section === section);
          const length = sectionRows[0]?.seats.length ?? 0;
          return <div className="venue-section" key={section} style={{ '--depth': `${depth(length)}px` } as CSSProperties}>
            {sections > 1 && <Heading className="venue-section-name">{sectionRows[0]?.name}</Heading>}
            {sectionRows.map(row => {
              const line = rows.indexOf(row);
              const action = rowAction?.(row.seats);
              const split = row.seats.length >= aisleFrom ? Math.ceil(row.seats.length / 2) : row.seats.length;
              const cell = (seat: Seat, index: number) => {
                const value = looks.get(seat.id)!;
                const { lift, tilt } = arc(index, row.seats.length, split);
                return <SeatCell key={seat.id} seat={seat} tone={value.tone} enabled={value.enabled} label={value.label}
                  tabbable={seat.id === active.current} swept={swept.has(seat.id)} lift={lift} tilt={tilt} />;
              };
              return <div className="venue-row" role="group" aria-label={`Row ${row.number}`} key={line}>
                {action ? <button type="button" className="venue-row-label" data-row={line} tabIndex={-1} title={action.label}
                  aria-label={action.label} disabled={action.disabled} onClick={action.run}>{row.number}</button>
                  : <span className="venue-row-label" aria-hidden="true">{row.number}</span>}
                <div className="venue-half">{row.seats.slice(0, split).map(cell)}</div>
                {split < row.seats.length && <div className="venue-half">{row.seats.slice(split).map((seat, index) => cell(seat, split + index))}</div>}
                <span className="venue-row-label venue-row-end" aria-hidden="true">{row.number}</span>
              </div>;
            })}
          </div>;
        })}
      </div>
    </div>
  </div>;
});

export function SeatLegend({ label }: { label: string }) {
  return <ul className="legend" aria-label={label}>
    {(['available', 'selected', 'held', 'booked', 'blocked'] as const).map(tone => <li key={tone}>
      <span className="legend-mark" data-tone={tone} aria-hidden="true" />{{ available: 'Available', selected: 'Selected', held: 'Held', booked: 'Booked', blocked: 'Blocked' }[tone]}
    </li>)}
  </ul>;
}
