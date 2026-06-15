// Shared SSE connection manager. Every metrics stream goes through here so that
// N subscribers to the same URL share ONE EventSource instead of each opening
// its own — which previously produced several parallel `stream?host_id=…`
// connections (StrictMode double-mounts, fast route swaps, multiple widgets).
//
// Connections are reference-counted and closed on a short delay after the last
// subscriber leaves, so a StrictMode unmount→remount (or a quick navigation
// back) reuses the live connection instead of churning a new one.

type MsgListener = (e: MessageEvent) => void;
type StatusListener = (connected: boolean) => void;

interface SharedConn {
  es: EventSource;
  msg: Set<MsgListener>;
  status: Set<StatusListener>;
  refs: number;
  closeTimer: ReturnType<typeof setTimeout> | null;
}

const conns = new Map<string, SharedConn>();

// CLOSE_DELAY: keep a refless connection alive briefly so an immediate remount
// (StrictMode / route swap) re-attaches instead of opening a fresh EventSource.
const CLOSE_DELAY = 300;

export interface StreamHandlers {
  onMetrics?: MsgListener;
  onStatus?: StatusListener;
}

/**
 * Subscribe to the shared EventSource for `url`. Returns an unsubscribe fn.
 * The first subscriber opens the connection; the last one schedules its close.
 */
export function subscribeStream(url: string, handlers: StreamHandlers): () => void {
  let conn = conns.get(url);
  if (conn?.closeTimer) {
    clearTimeout(conn.closeTimer);
    conn.closeTimer = null;
  }
  if (!conn) {
    const es = new EventSource(url, { withCredentials: true });
    const c: SharedConn = { es, msg: new Set(), status: new Set(), refs: 0, closeTimer: null };
    es.addEventListener('metrics', (e) => {
      const ev = e as MessageEvent;
      c.msg.forEach((fn) => fn(ev));
      c.status.forEach((fn) => fn(true));
    });
    es.onopen = () => c.status.forEach((fn) => fn(true));
    es.onerror = () => {
      // The browser auto-reconnects per the EventSource spec; surface "down" so
      // callers (e.g. the machine list) fall back to polling until it resumes.
      c.status.forEach((fn) => fn(false));
    };
    conns.set(url, c);
    conn = c;
  }

  conn.refs += 1;
  if (handlers.onMetrics) conn.msg.add(handlers.onMetrics);
  if (handlers.onStatus) conn.status.add(handlers.onStatus);

  let released = false;
  return () => {
    if (released) return;
    released = true;
    const c = conns.get(url);
    if (!c) return;
    if (handlers.onMetrics) c.msg.delete(handlers.onMetrics);
    if (handlers.onStatus) c.status.delete(handlers.onStatus);
    c.refs -= 1;
    if (c.refs <= 0 && !c.closeTimer) {
      c.closeTimer = setTimeout(() => {
        c.es.close();
        conns.delete(url);
      }, CLOSE_DELAY);
    }
  };
}
