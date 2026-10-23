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
      } catch (e) {
        if (alive) onError(e);
      } finally {
        renewing = false;
      }
    };
    void register();
    const heartbeat = setInterval(() => {
      if (!current) return;
      void client.heartbeat(current).catch(() => {
        current = null;
        setPage(null);
        void register();
      });
    }, 5000);
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
