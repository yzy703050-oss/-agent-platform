import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { WebSearchResult } from "./WebSearchResult";

describe("联网搜索来源", () => {
  it("renders safe sources, failures and malformed data without HTML", () => {
    const { rerender } = render(
      <WebSearchResult
        content={JSON.stringify({
          status: "ok",
          message: "找到资料",
          results: [
            {
              title: "Eino 官方文档",
              url: "https://www.cloudwego.io/docs/eino/",
              summary: "公开文档摘要",
            },
            {
              title: "<img src=x onerror=alert(1)>",
              url: "javascript:alert(1)",
              summary: "不可信内容",
            },
            {
              title: "credential",
              url: "https://u:p@example.org/",
              summary: "",
            },
          ],
        })}
      />,
    );
    expect(screen.getByRole("link", { name: "Eino 官方文档" })).toHaveAttribute(
      "href",
      "https://www.cloudwego.io/docs/eino/",
    );
    expect(screen.getAllByRole("link")).toHaveLength(1);
    expect(screen.getByText("公开文档摘要")).toBeInTheDocument();
    expect(document.querySelector("img")).toBeNull();
    rerender(
      <WebSearchResult
        content={JSON.stringify({
          status: "search_unavailable",
          message: "此次搜索未成功",
          results: [],
        })}
      />,
    );
    expect(screen.getByText("此次搜索未成功")).toBeInTheDocument();
    rerender(<WebSearchResult content="broken JSON" />);
    expect(screen.getByText("broken JSON")).toBeInTheDocument();
    rerender(<WebSearchResult content='{"status":"ok","results":[null]}' />);
    expect(screen.getByText(/"results"/)).toBeInTheDocument();
  });
});
