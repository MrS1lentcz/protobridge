// Protobridge broadcast SSE client helper.
//
// Wraps the browser's EventSource with ticket-based auth and automatic
// re-issuance on reconnect. Tickets are one-shot and short-lived — the
// helper transparently fetches a new ticket whenever the connection drops
// and EventSource reopens.
//
// Usage:
//
//   import { openEventStream } from "./events";
//
//   const stream = openEventStream<MyEnvelope>({
//     url: "/api/events/tasks",
//     ticketUrl: "/api/events/ticket",
//     getAuthHeaders: () => ({ Authorization: `Bearer ${token}` }),
//     onMessage: (env) => console.log(env.subject, env.event),
//   });
//
//   // Later, when leaving the page:
//   stream.close();

export interface EventEnvelope<E = unknown> {
  subject: string;
  labels?: Record<string, string>;
  event: E;
}

export interface EventStreamOptions<E = unknown> {
  /** SSE endpoint (e.g. "/api/events/tasks"). Required. */
  url: string;
  /** Ticket issuer endpoint (POST). Required. */
  ticketUrl: string;
  /**
   * Returns headers used on the ticket POST. Called fresh on every
   * reconnect so rotated/refreshed tokens are picked up automatically.
   */
  getAuthHeaders: () => Record<string, string> | Promise<Record<string, string>>;
  /** Envelope handler. Called once per server message. */
  onMessage: (env: EventEnvelope<E>) => void;
  /** Optional connection-state callback. */
  onOpen?: () => void;
  /** Optional error callback. Errors are non-fatal; the helper retries. */
  onError?: (err: unknown) => void;
  /** Optional per-subject router (UX filter). */
  filter?: (env: EventEnvelope<E>) => boolean;
  /** Reconnect backoff (ms). Defaults to [500, 1000, 2000, 5000, 10000]. */
  backoffMs?: number[];
}

export interface EventStream {
  close(): void;
}

const DEFAULT_BACKOFF = [500, 1000, 2000, 5000, 10000];

export function openEventStream<E = unknown>(opts: EventStreamOptions<E>): EventStream {
  let closed = false;
  let attempt = 0;
  let es: EventSource | null = null;
  const backoff = opts.backoffMs ?? DEFAULT_BACKOFF;

  const connect = async () => {
    if (closed) return;
    try {
      const headers = await opts.getAuthHeaders();
      const resp = await fetch(opts.ticketUrl, { method: "POST", headers });
      if (!resp.ok) throw new Error(`ticket issue failed: ${resp.status}`);
      const { ticket } = (await resp.json()) as { ticket: string };
      if (closed) return;

      const sep = opts.url.includes("?") ? "&" : "?";
      es = new EventSource(`${opts.url}${sep}ticket=${encodeURIComponent(ticket)}`);

      es.onopen = () => {
        attempt = 0;
        opts.onOpen?.();
      };
      es.onmessage = (ev) => {
        let env: EventEnvelope<E>;
        try {
          env = JSON.parse(ev.data) as EventEnvelope<E>;
        } catch (err) {
          opts.onError?.(err);
          return;
        }
        if (opts.filter && !opts.filter(env)) return;
        opts.onMessage(env);
      };
      es.onerror = (err) => {
        opts.onError?.(err);
        // EventSource auto-reconnects, but reuses the same URL — and our
        // ticket is one-shot (already consumed on first connect). Close
        // the current stream and re-issue a fresh ticket on a delay.
        es?.close();
        es = null;
        if (closed) return;
        const delay = backoff[Math.min(attempt, backoff.length - 1)];
        attempt++;
        setTimeout(connect, delay);
      };
    } catch (err) {
      opts.onError?.(err);
      if (closed) return;
      const delay = backoff[Math.min(attempt, backoff.length - 1)];
      attempt++;
      setTimeout(connect, delay);
    }
  };

  void connect();

  return {
    close() {
      closed = true;
      es?.close();
      es = null;
    },
  };
}
