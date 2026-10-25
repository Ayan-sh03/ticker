import { useEffect, useRef, useState } from 'react';
import { ApiError, request } from './api';

// Every private read asserts the account that owns this view and drops old data
// before a recheck, including when membership has been revoked.
export function usePrivateResource<T>(path: string | null, userID: string, onSessionExpired: () => void) {
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<{ data?: T; error?: string; forbidden?: boolean }>({});
  const expire = useRef(onSessionExpired);
  expire.current = onSessionExpired;
  useEffect(() => {
    if (!path) return;
    const controller = new AbortController();
    setState({});
    request<T>(path, { signal: controller.signal, headers: { 'X-Ticker-User': userID } }).then(data => {
      if (!controller.signal.aborted) setState({ data });
    }).catch(failure => {
      if (controller.signal.aborted) return;
      if (failure instanceof ApiError && failure.status === 401) { setState({}); expire.current(); return; }
      const forbidden = failure instanceof ApiError && failure.status === 403;
      setState({ forbidden, error: forbidden ? 'You no longer have access to this organizer workspace.'
        : failure instanceof ApiError ? failure.message : 'Unable to connect. Please try again.' });
    });
    return () => controller.abort();
  }, [path, userID, revision]);
  return { ...state, retry: () => setRevision(value => value + 1), replace: (data: T) => setState({ data }),
    deny: () => setState({ forbidden: true, error: 'You no longer have access to this organizer workspace.' }) };
}
