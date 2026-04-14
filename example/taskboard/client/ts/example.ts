// End-to-end usage of the broadcast SSE helper.
//
// The server exposes:
//   POST /api/events/ticket      — exchanges your bearer/cookie for a ticket
//   GET  /api/events/tasks       — SSE stream (pass ticket as ?ticket=)
//
// This file is the minimal TypeScript glue a FE app needs to consume the
// stream. No external deps — just the browser's EventSource + fetch.

import { openEventStream, EventEnvelope } from "./events";

// Typed event union — matches the oneof variants on the server-side
// TaskBroadcastEnvelope. In a real app these would come from generated
// TS bindings (asyncapi codegen) instead of being hand-written.
type TaskCreated = { task_id: string; title: string };
type TaskUpdated = { task_id: string; status: string };
type TaskEvent = TaskCreated | TaskUpdated;

function loadBearerToken(): string {
  // App-specific: localStorage, in-memory cache after login, cookie, …
  return localStorage.getItem("token") ?? "";
}

const stream = openEventStream<TaskEvent>({
  url: "/api/events/tasks",
  ticketUrl: "/api/events/ticket",

  // Called on every (re)connect — rotated tokens are picked up
  // automatically without touching the stream helper.
  getAuthHeaders: () => ({
    Authorization: `Bearer ${loadBearerToken()}`,
  }),

  onOpen: () => console.info("events: connected"),

  onError: (err) => console.warn("events:", err),

  // Optional UX filter layered on top of the server-side label filter.
  // The server already enforces tenant/role; this narrows to the screen
  // the user is currently looking at.
  filter: (env) => env.labels?.project_id === currentProjectId(),

  onMessage: (env: EventEnvelope<TaskEvent>) => {
    switch (env.subject) {
      case "task_created":
        renderCreated(env.event as TaskCreated);
        break;
      case "task_updated":
        renderUpdated(env.event as TaskUpdated);
        break;
      default:
        // Forward-compat: unknown subjects are ignored silently.
        break;
    }
  },
});

// When the component/page unmounts:
//   stream.close();

// --- placeholders so this file compiles as a standalone example ---
declare function currentProjectId(): string;
declare function renderCreated(ev: TaskCreated): void;
declare function renderUpdated(ev: TaskUpdated): void;
export { stream };
