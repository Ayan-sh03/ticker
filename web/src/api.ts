import { useEffect, useState } from 'react';

export interface Event {
  id: string;
  title: string;
  description: string;
  venue_name: string;
  price_minor: number;
  currency: string;
  starts_at: string;
}

export interface Seat {
  id: string;
  section: string;
  row_number: number;
  seat_number: number;
  label: string;
  status: 'AVAILABLE' | 'HELD' | 'SOLD' | 'BLOCKED';
}

export interface Session {
  user: { id: string; email: string; display_name: string };
  expires_at: string;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api${path}`, {
    ...options,
    credentials: 'same-origin',
    signal: options.signal
      ? AbortSignal.any([options.signal, AbortSignal.timeout(8000)])
      : AbortSignal.timeout(8000),
    headers: { 'Content-Type': 'application/json', 'X-Ticker-Request': '1', ...options.headers },
  });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    throw new ApiError(response.status, body?.error?.message ?? 'The request failed. Please try again.');
  }
  return response.status === 204 ? undefined as T : response.json();
}

export function useResource<T>(path: string | null, poll = 0) {
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<{ path: string | null; data?: T; error?: string }>({ path });
  useEffect(() => {
    if (!path) return;
    const endpoint = path;
    let controller: AbortController | undefined;
    let timer: ReturnType<typeof setTimeout>;
    let stopped = false;
    async function load() {
      clearTimeout(timer);
      controller?.abort();
      const active = new AbortController();
      controller = active;
      try {
        const data = await request<T>(endpoint, { signal: active.signal });
        if (!active.signal.aborted) setState({ path, data });
      } catch (error) {
        if (!active.signal.aborted) setState(previous => ({
          path, data: previous.path === path ? previous.data : undefined,
          error: error instanceof ApiError ? error.message : 'Unable to connect. Please try again.',
        }));
      } finally {
        if (!stopped && !active.signal.aborted && poll && !document.hidden) timer = setTimeout(load, poll);
      }
    }
    function resume() { if (!document.hidden) void load(); else { clearTimeout(timer); controller?.abort(); } }
    void load();
    if (poll) {
      document.addEventListener('visibilitychange', resume);
      window.addEventListener('online', resume);
      window.addEventListener('focus', resume);
    }
    return () => {
      stopped = true;
      clearTimeout(timer);
      controller?.abort();
      document.removeEventListener('visibilitychange', resume);
      window.removeEventListener('online', resume);
      window.removeEventListener('focus', resume);
    };
  }, [path, poll, revision]);
  return { ...(state.path === path ? state : {}), retry: () => setRevision(value => value + 1) };
}

export function price(event: Event) {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency: event.currency }).format(event.price_minor / 100);
}

export function date(event: Event) {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(event.starts_at));
}
