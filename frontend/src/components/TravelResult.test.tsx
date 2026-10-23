import { render, screen, cleanup } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { TravelResult } from "./TravelResult";
afterEach(cleanup);
it("retains demo labels on query, saved selection and policy evidence", () => {
  const content = JSON.stringify({
    trip: {
      plan: {
        revision: 2,
        tasks: [
          {
            id: "t1",
            origin: "上海",
            destination: "杭州",
            departure_date: "2026-10-25",
            train: {
              candidate: { name: "G1001", price_fen: 10000, is_demo: true },
            },
          },
        ],
      },
    },
    evidence: [{ status: "ok", is_demo: true, candidates: [] }],
  });
  const view = render(<TravelResult content={content} />);
  expect(screen.getByText(/演示数据/)).toBeTruthy();
  view.rerender(
    <TravelResult
      content={JSON.stringify({
        evidence: { status: "ok", is_demo: true, fragments: [] },
        reason: "检索命中",
      })}
    />,
  );
  expect(screen.getByText(/演示政策/)).toBeTruthy();
});
