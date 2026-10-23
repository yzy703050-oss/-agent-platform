import { useEffect } from "react";
import type { PlatformAPI, RunEvent } from "../api/client";
export function useRunEvents(
  client: PlatformAPI,
  runID: string | undefined,
  enabled: boolean,
  onEvent: (e: RunEvent) => void,
  onReconnect: () => Promise<void>,
) {
  useEffect(() => {
    if (!runID || !enabled) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    void (async () => {
      while (!controller.signal.aborted) {
        try {
          await client.subscribe(runID, onEvent, controller.signal);
        } catch {
          if (controller.signal.aborted) return;
        }
        if (controller.signal.aborted) return;
        await onReconnect();
        await new Promise<void>((resolve) => {
          timer = setTimeout(resolve, 1500);
          controller.signal.addEventListener(
            "abort",
            () => {
              clearTimeout(timer);
              resolve();
            },
            { once: true },
          );
        });
      }
    })();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [client, runID, enabled, onEvent, onReconnect]);
}
