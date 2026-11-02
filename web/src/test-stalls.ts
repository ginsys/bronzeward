// Stubs of fetch that stall or break the way a network does, for the attempt bound of client
// contracts §8.4 item 8 (ginsys/bronzeward#487). They end when the attempt's abort signal fires, as a
// browser's fetch does: a request still waiting for headers rejects, and a body still being read errors.

export const abortError = () => new DOMException("The operation was aborted.", "AbortError");

// A fetch whose headers never arrive.
export function noHeaders(init?: RequestInit): Promise<Response> {
  return new Promise((_, reject) => {
    const signal = init?.signal;
    if (signal?.aborted) reject(abortError());
    else signal?.addEventListener("abort", () => reject(abortError()), { once: true });
  });
}

// The headers arrive, the body never ends.
export function endlessBody(init: RequestInit | undefined, status: number, headers: Record<string, string>): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(ctl) {
      init?.signal?.addEventListener("abort", () => ctl.error(abortError()), { once: true });
    },
  });
  return new Response(stream, { status, headers });
}

// The headers arrive, then the network cuts the body.
export function cutBody(status: number, headers: Record<string, string>): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(ctl) {
      ctl.error(new TypeError("network error"));
    },
  });
  return new Response(stream, { status, headers });
}

// The headers arrive at once and the whole body after `ms` of the (fake) clock.
export function lateBody(status: number, headers: Record<string, string>, body: unknown, ms: number): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(ctl) {
      setTimeout(() => {
        ctl.enqueue(new TextEncoder().encode(JSON.stringify(body)));
        ctl.close();
      }, ms);
    },
  });
  return new Response(stream, { status, headers });
}
