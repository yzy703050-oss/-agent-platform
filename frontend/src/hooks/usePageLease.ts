import { useEffect, useState } from "react";
import type { PageLease, PlatformAPI } from "../api/client";
export function usePageLease(
  client: PlatformAPI,
  userID: string | undefined,
  onError: (e: unknown) => void,
) {
  const [page, setPage] = useState<PageLease | null>(null);
  useEffect(() => {
    if (!userID) {
      setPage(null);
      return;
    }
    let alive = true;
    let current: PageLease | null = null;
    let renewing = false;
    let heartbeat: ReturnType<typeof setInterval> | undefined;
    const register = async () => {
      if (renewing) return;
      renewing = true;
      try {
        const p = await client.registerPage();
        if (!alive) {
          void client.leave(p).catch(() => {});
          return;
        }
        current = p;
        setPage(p);
        clearInterval(heartbeat);
        heartbeat = setInterval(beat, p.heartbeat_ms || 5000);
      } catch (e) {
        if (alive) onError(e);
      } finally {
        renewing = false;
      }
    };
    const beat = () => {
      if (!current) {
        void register();
        return;
      }
      void client.heartbeat(current).catch(() => {
        if (!alive) return;
        current = null;
        setPage(null);
        void register();
      });
    };
    heartbeat = setInterval(beat, 5000);
    void register();
    const leave = () => {
      if (current) void client.leave(current).catch(() => {});
    };
    window.addEventListener("pagehide", leave);
    return () => {
      alive = false;
      clearInterval(heartbeat);
      window.removeEventListener("pagehide", leave);
      leave();
      setPage(null);
    };
  }, [client, userID, onError]);
  return page;
}
