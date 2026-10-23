import type { RunEvent } from "./client";
export async function readEvents(
  response: Response,
  onEvent: (event: RunEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  const reader = response.body?.getReader();
  if (!reader) throw new Error("stream unavailable");
  const decoder = new TextDecoder();
  let buffer = "";
  let lastSeq = 0;
  try {
    for (;;) {
      if (signal.aborted) return;
      const chunk = await reader.read();
      if (chunk.done) return;
      buffer += decoder
        .decode(chunk.value, { stream: true })
        .replace(/\r\n/g, "\n");
      if (buffer.length > 1_000_000) throw new Error("stream frame too large");
      let end: number;
      while ((end = buffer.indexOf("\n\n")) >= 0) {
        const frame = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        const data = frame
          .split("\n")
          .filter((line) => line.startsWith("data:"))
          .map((line) => line.slice(5).trimStart())
          .join("\n");
        if (!data) continue;
        const event = JSON.parse(data) as RunEvent;
        if (event.seq && event.seq <= lastSeq) continue;
        if (event.seq) lastSeq = event.seq;
        onEvent(event);
      }
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
