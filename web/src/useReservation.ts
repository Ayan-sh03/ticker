import { useEffect, useRef, useState } from 'react';
import { ApiError, request, type Booking, type Reservation, type Session } from './api';

type Pending = { kind: 'reserve'; key: string; seatID: string }
  | { kind: 'confirm'; key: string; reservationID: string }
  | { kind: 'cancel'; reservationID: string };
type Saved = { reservationID?: string; bookingID?: string; pending?: Pending };
class StorageError extends Error {}
const uuid = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);

function readSaved(key: string): Saved {
  const saved = JSON.parse(sessionStorage.getItem(key) ?? '{}');
  if (!saved || typeof saved !== 'object' || Array.isArray(saved)
    || (saved.reservationID !== undefined && !uuid(saved.reservationID))
    || (saved.bookingID !== undefined && !uuid(saved.bookingID))) throw new Error('Invalid saved reservation');
  const pending = saved.pending;
  if (pending && !((pending.kind === 'reserve' && uuid(pending.key) && uuid(pending.seatID))
    || (pending.kind === 'confirm' && uuid(pending.key) && uuid(pending.reservationID))
    || (pending.kind === 'cancel' && uuid(pending.reservationID)))) throw new Error('Invalid saved request');
  return saved;
}

// Only identifiers and the exact unresolved operation live in storage, scoped to
// the authenticated owner and event. Never replace a pending operation's key.
export function useReservation(eventID: string, userID: string | undefined, onSessionExpired: () => void) {
  eventID = eventID.toLowerCase();
  const storageKey = `ticker:reservation:v1:${userID}:${eventID}`;
  const saved = useRef<Saved>({});
  const alive = useRef(false);
  const working = useRef(false);
  const mutating = useRef(false);
  const storageFailed = useRef(false);
  const controller = useRef<AbortController | null>(null);
  const expiredCallback = useRef(onSessionExpired);
  expiredCallback.current = onSessionExpired;
  const [reservation, setReservation] = useState<Reservation>();
  const [booking, setBooking] = useState<Booking>();
  const [pending, setPending] = useState<Pending>();
  const [ready, setReady] = useState(!userID);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [clock, setClock] = useState<{ remaining: number; at: number }>();
  const [now, setNow] = useState(performance.now());
  const seconds = clock ? Math.max(0, Math.ceil((clock.remaining - (now - clock.at)) / 1000)) : 0;

  function persist(value: Saved) {
    if (!alive.current) return false;
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(value));
      saved.current = value;
      setPending(value.pending);
      return true;
    } catch {
      storageFailed.current = true;
      setReady(false);
      setError('Browser storage is unavailable. Enable it and reload to recover requests safely.');
      return false;
    }
  }

  function accept(value: Reservation, duration: number) {
    if (!alive.current) return;
    setReservation(value);
    // A fresh GET supplies database time. Subtract the full round trip so the
    // countdown never grants extra time; stored idempotency timestamps are stale.
    setClock({ remaining: Date.parse(value.expires_at) - Date.parse(value.server_time) - duration, at: performance.now() });
    setNow(performance.now());
  }

  async function read(signal: AbortSignal) {
    if (!saved.current.reservationID) return;
    const started = performance.now();
    const { reservation: value } = await request<{ reservation: Reservation }>(`/reservations/${saved.current.reservationID}`, { signal, headers: { 'X-Ticker-User': userID! } });
    if (!alive.current || signal.aborted) return;
    if (value.event_id.toLowerCase() !== eventID) throw new Error('Reservation does not match this event');
    accept(value, performance.now() - started);
    if (value.booking_id) {
      const { booking: receipt } = await request<{ booking: Booking }>(`/bookings/${value.booking_id}`, { signal, headers: { 'X-Ticker-User': userID! } });
      if (alive.current && !signal.aborted) setBooking(receipt);
    }
  }

  async function replay(operation: Pending, signal: AbortSignal) {
    if (operation.kind === 'confirm') {
      const { booking: receipt } = await request<{ booking: Booking }>(`/reservations/${operation.reservationID}/confirm`, {
        method: 'POST', headers: { 'Idempotency-Key': operation.key, 'X-Ticker-User': userID! }, signal,
      });
      if (signal.aborted) return;
      if (!persist({ reservationID: operation.reservationID, bookingID: receipt.id })) throw new StorageError();
      setBooking(receipt);
      setReservation(previous => previous ? { ...previous, status: 'CONFIRMED', booking_id: receipt.id } : previous);
    } else {
      const result = await request<{ reservation: Reservation }>(operation.kind === 'reserve' ? '/reservations' : `/reservations/${operation.reservationID}`, {
        method: operation.kind === 'reserve' ? 'POST' : 'DELETE', signal,
        headers: { 'X-Ticker-User': userID!, ...(operation.kind === 'reserve' ? { 'Idempotency-Key': operation.key } : {}) },
        ...(operation.kind === 'reserve' ? { body: JSON.stringify({ event_id: eventID, seat_ids: [operation.seatID] }) } : {}),
      });
      if (signal.aborted) return;
      if (!persist({ reservationID: result.reservation.id })) throw new StorageError();
      // Keep identity visible while fetching a fresh clock after reserve replay.
      setReservation(result.reservation);
      setClock(undefined);
    }
    await read(signal);
  }

  async function run(operation?: Pending, silent = false) {
    if (!alive.current || working.current || storageFailed.current) return;
    working.current = true;
    mutating.current = !!operation;
    if (!silent) { setBusy(true); setError(''); }
    const active = new AbortController();
    controller.current = active;
    try {
      // Cookies can change in another tab. Never replay this account's saved
      // request until the server verifies that it is still the signed-in owner.
      const identity = await request<Session>('/me', { signal: active.signal, headers: { 'X-Ticker-User': userID! } });
      if (identity.user.id !== userID) throw new ApiError(401, 'The signed-in account changed.');
      if (operation) await replay(operation, active.signal); else await read(active.signal);
      if (alive.current && !active.signal.aborted) setError('');
    } catch (failure) {
      if (!alive.current || active.signal.aborted) return;
      if (failure instanceof StorageError) return;
      if (failure instanceof ApiError && failure.status === 401) {
        expiredCallback.current();
        return;
      }
      // Network errors, response parsing failures and 5xx may follow a commit.
      // Preserve the pending request for replay, including across refresh.
      if (operation && failure instanceof ApiError && failure.status >= 400 && failure.status < 500) {
        if (!persist({ ...saved.current, pending: undefined })) return;
        setError(failure.code === 'seat_unavailable' ? 'That seat was taken. Choose another.'
          : failure.code === 'reservation_not_confirmable' ? 'This hold can no longer be confirmed. Checking its status.' : failure.message);
        try { await read(active.signal); } catch { /* Keep the original failure and offer retry. */ }
      } else {
        setError(saved.current.pending ? 'The result is unknown. Retry the saved request to recover its outcome.'
          : 'Unable to refresh your reservation or receipt. Retry before continuing.');
        setClock(undefined);
      }
    } finally {
      if (controller.current === active) {
        working.current = false;
        mutating.current = false;
        if (alive.current) setBusy(false);
      }
    }
  }

  useEffect(() => {
    alive.current = true;
    setBusy(false);
    if (!userID) return () => { alive.current = false; };
    try {
      saved.current = readSaved(storageKey);
      // Verify writes before enabling any action, even when there is no record.
      if (!persist(saved.current)) return () => { alive.current = false; };
      storageFailed.current = false;
      setError('');
      setReady(true);
      void run(saved.current.pending);
    } catch {
      storageFailed.current = true;
      setReady(false);
      setError('Saved request recovery is unavailable. Check browser storage and reload before reserving.');
    }
    const resume = () => { if (!document.hidden) void run(saved.current.pending); };
    const poll = window.setInterval(() => {
      if (!document.hidden && !saved.current.pending && saved.current.reservationID) void run(undefined, true);
    }, 2000);
    const tick = window.setInterval(() => setNow(performance.now()), 1000);
    document.addEventListener('visibilitychange', resume);
    window.addEventListener('online', resume);
    window.addEventListener('focus', resume);
    return () => {
      alive.current = false;
      controller.current?.abort();
      controller.current = null;
      working.current = false;
      mutating.current = false;
      window.clearInterval(poll);
      window.clearInterval(tick);
      document.removeEventListener('visibilitychange', resume);
      window.removeEventListener('online', resume);
      window.removeEventListener('focus', resume);
    };
    // EventView is keyed by account and event; this effect owns that lifetime.
  }, [storageKey, userID]);

  useEffect(() => {
    if (reservation?.status === 'HELD' && clock && seconds === 0 && !pending) void run();
  }, [seconds, reservation?.status, pending]);

  function interruptRead() {
    if (working.current && mutating.current) return false;
    controller.current?.abort();
    controller.current = null;
    working.current = false;
    return true;
  }

  function start(operation: Pending) {
    if (!userID || !ready || saved.current.pending || !interruptRead()) return;
    if (persist({ ...saved.current, pending: operation })) void run(operation);
  }

  return {
    reservation, booking, pending, seconds, ready, busy, error, clockReady: !!clock,
    hasRecord: !!saved.current.reservationID,
    canConfirm: reservation?.status === 'HELD' && !!clock && seconds > 0 && !busy && !pending && !error,
    reserve: (seatID: string) => {
      if (!saved.current.reservationID) start({ kind: 'reserve', key: crypto.randomUUID(), seatID });
    },
    confirm: () => { if (reservation && clock && seconds > 0) start({ kind: 'confirm', key: crypto.randomUUID(), reservationID: reservation.id }); },
    cancel: () => { if (reservation?.status === 'HELD') start({ kind: 'cancel', reservationID: reservation.id }); },
    retry: () => { void run(saved.current.pending); },
    clear: () => {
      if (!saved.current.pending && reservation && reservation.status !== 'HELD' && interruptRead() && persist({})) {
        setReservation(undefined); setBooking(undefined); setClock(undefined); setError('');
      }
    },
  };
}
