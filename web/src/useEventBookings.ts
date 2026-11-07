import { useEffect, useRef, useState } from 'react';
import { ApiError, request, type Booking } from './api';

export type EventBookings = { bookings: Booking[]; next_before?: string; total_bookings: number; booked_seat_ids: string[] };
type State = { key: string; data?: EventBookings; error?: string; loading: boolean };

// Keep the visible page in place during refresh, but never accept an old page
// or account's response. The summary covers owned seats beyond this page too.
export function useEventBookings(eventID: string | null, userID: string | undefined, before: string, receiptKey: string, onSessionExpired: () => void) {
  const path = eventID && userID ? `/bookings?event_id=${encodeURIComponent(eventID)}${before ? `&before=${encodeURIComponent(before)}` : ''}` : null;
  const key = `${userID}:${path}`;
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<State>({ key, loading: !!path });
  const expire = useRef(onSessionExpired);
  expire.current = onSessionExpired;

  useEffect(() => {
    if (!path || !userID) return;
    let controller: AbortController | undefined;
    let timer: ReturnType<typeof setTimeout>;
    let stopped = false;
    async function load() {
      clearTimeout(timer);
      controller?.abort();
      const active = new AbortController();
      controller = active;
      setState(previous => ({ ...(previous.key === key ? previous : {}), key, loading: true }));
      try {
        const data = await request<EventBookings>(path!, { signal: active.signal, headers: { 'X-Ticker-User': userID! } });
        if (!active.signal.aborted) setState({ key, data, loading: false });
      } catch (failure) {
        if (active.signal.aborted) return;
        if (failure instanceof ApiError && failure.status === 401) {
          stopped = true;
          setState({ key, loading: false });
          expire.current();
          return;
        }
        setState(previous => {
          const data = failure instanceof ApiError && failure.status === 403 ? undefined : previous.key === key ? previous.data : undefined;
          return { key, data, error: data ? 'Your bookings could not refresh. Showing the last loaded page.' : 'Your bookings could not be loaded.', loading: false };
        });
      } finally {
        if (!stopped && !active.signal.aborted && !document.hidden) timer = setTimeout(load, 5000);
      }
    }
    function resume() {
      if (stopped) return;
      if (!document.hidden) void load();
      else { clearTimeout(timer); controller?.abort(); }
    }
    resume();
    document.addEventListener('visibilitychange', resume);
    window.addEventListener('online', resume);
    window.addEventListener('focus', resume);
    return () => {
      stopped = true;
      clearTimeout(timer);
      controller?.abort();
      document.removeEventListener('visibilitychange', resume);
      window.removeEventListener('online', resume);
      window.removeEventListener('focus', resume);
    };
  }, [path, userID, key, receiptKey, revision]);

  const current: State = path && state.key === key ? state : { key, loading: !!path };
  return { ...current, retry: () => setRevision(value => value + 1) };
}
