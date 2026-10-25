import {
  render,
  screen,
  waitFor,
  fireEvent,
  act,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach } from "vitest";
import App from "./App";
import type { PlatformAPI, Conversation, Run } from "./api/client";
beforeEach(() => localStorage.clear());

function fixture() {
  const history: Conversation[] = [
    { id: "old", agent_type: "general", title: "暂停的对话", created_at: "" },
    { id: "trip", agent_type: "travel", title: "差旅历史", created_at: "" },
  ];
  const paused: Run = {
    id: "run-old",
    conversation_id: "old",
    page_id: "previous-document",
    status: "paused",
    attempt: 1,
    version: 3,
    context_eligible: true,
    error_code: "",
    partial_output: "已保存的输出",
  };
  const client = {
    me: vi.fn().mockResolvedValue({ id: "user", username: "demo" }),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn().mockResolvedValue(undefined),
    settings: vi
      .fn()
      .mockResolvedValue({ theme: "system", reply_preference: "" }),
    saveSettings: vi.fn(),
    agents: vi.fn().mockResolvedValue({
      items: [
        { id: "general", available: true },
        { id: "travel", available: false, reason: "not_connected" },
      ],
    }),
    conversations: vi
      .fn()
      .mockResolvedValue({ items: history, next_cursor: "" }),
    createConversation: vi.fn(async (agent: string) => ({
      id: "new-" + agent,
      agent_type: agent,
      title: "新对话",
      created_at: "",
    })),
    renameConversation: vi.fn(),
    deleteConversation: vi.fn(),
    messages: vi.fn(async (id: string) => ({
      items:
        id === "old"
          ? [
              {
                id: "msg",
                conversation_id: "old",
                run_id: "run-old",
                role: "assistant",
                content: "已保存的输出",
                ordinal: 1,
                tool_calls: [],
                tool_call_id: "",
              },
            ]
          : [],
      next_after: 1,
    })),
    run: vi.fn().mockResolvedValue(paused),
    submit: vi.fn(async () => ({
      ...paused,
      id: "new-run",
      status: "running",
    })),
    action: vi.fn(async () => ({ ...paused, status: "running", attempt: 2 })),
    registerPage: vi.fn().mockResolvedValue({
      page_id: "new-document",
      page_token: "page-secret",
      expires_at: "",
    }),
    heartbeat: vi.fn().mockResolvedValue(undefined),
    leave: vi.fn().mockResolvedValue(undefined),
    subscribe: vi.fn(
      (_id: string, _on: unknown, signal: AbortSignal) =>
        new Promise<void>((resolve) =>
          signal.addEventListener("abort", () => resolve(), { once: true }),
        ),
    ),
  } as unknown as PlatformAPI;
  return { client, paused };
}
async function openPaused(client: PlatformAPI) {
  render(<App client={client} />);
  await userEvent.click(
    await screen.findByRole("button", { name: "暂停的对话" }),
  );
  await screen.findByRole("button", { name: "继续任务" });
}
describe("平台交互", () => {
  it("shows web search events and only renders sources for matched search calls", async () => {
    const { client, paused } = fixture();
    paused.status = "running";
    const content = JSON.stringify({
      status: "ok",
      message: "搜索完成",
      results: [
        {
          title: "Eino 来源",
          url: "https://www.cloudwego.io/docs/eino/",
          summary: "官方摘要",
        },
      ],
    });
    const base = {
      conversation_id: "old",
      run_id: "run-old",
      content: "",
      tool_call_id: "",
      tool_calls: [],
    };
    vi.mocked(client.messages).mockResolvedValue({
      items: [
        {
          ...base,
          id: "call",
          ordinal: 1,
          role: "assistant",
          tool_calls: [
            { id: "s", name: "web_search", arguments: '{"query":"Eino"}' },
            { id: "settings", name: "get_my_settings", arguments: "{}" },
          ],
        },
        {
          ...base,
          id: "search",
          ordinal: 2,
          role: "tool",
          tool_call_id: "s",
          content,
        },
        {
          ...base,
          id: "settings-result",
          ordinal: 3,
          role: "tool",
          tool_call_id: "settings",
          content,
        },
      ],
      next_after: 3,
    });
    render(<App client={client} />);
    await userEvent.click(
      await screen.findByRole("button", { name: "暂停的对话" }),
    );
    expect(
      await screen.findByRole("link", { name: "Eino 来源" }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "Eino 来源" })).toHaveLength(1);
    await waitFor(() => expect(client.subscribe).toHaveBeenCalled());
    const listener = vi.mocked(client.subscribe).mock.calls[0][1];
    const emit = (type: string, attempt = 1) =>
      act(() =>
        listener({
          run_id: "run-old",
          attempt,
          seq: 1,
          type,
          payload: { name: "web_search" },
        }),
      );
    emit("tool.started");
    expect(screen.getByText(/正在联网搜索/)).toBeInTheDocument();
    emit("tool.failed", 0);
    expect(screen.queryByText(/联网搜索失败/)).not.toBeInTheDocument();
    emit("tool.completed");
    expect(screen.getByText(/联网搜索已返回结果/)).toBeInTheDocument();
    emit("tool.failed");
    expect(screen.getByText(/联网搜索失败/)).toBeInTheDocument();
  });
  it("readyTravelWorksWithoutGeneral", async () => {
    const { client, paused } = fixture();
    vi.mocked(client.submit).mockResolvedValue({
      ...paused,
      id: "new-run",
      conversation_id: "new-travel",
      status: "running",
    });
    vi.mocked(client.agents).mockResolvedValue({
      items: [
        { id: "general", available: false },
        { id: "travel", available: true },
      ],
    });
    render(<App client={client} />);
    await userEvent.click(
      await screen.findByRole("button", { name: "差旅助手" }),
    );
    const input = screen.getByRole("textbox", { name: "输入消息" });
    await waitFor(() => expect(input).toBeEnabled());
    await userEvent.type(input, "明天去杭州出差");
    await userEvent.click(screen.getByRole("button", { name: "发送" }));
    await waitFor(() =>
      expect(client.submit).toHaveBeenCalledWith(
        "new-travel",
        "明天去杭州出差",
        expect.anything(),
        expect.any(String),
      ),
    );
    await waitFor(() => expect(client.subscribe).toHaveBeenCalled());
    const listener = vi.mocked(client.subscribe).mock.calls[0][1];
    act(() =>
      listener({
        run_id: "new-run",
        attempt: 1,
        seq: 1,
        type: "travel.phase",
        payload: { phase: "validating", plan_id: "p1" },
      }),
    );
    expect(await screen.findByText(/正在校验行程/)).toBeInTheDocument();
    vi.mocked(client.run).mockResolvedValue({
      ...paused,
      id: "new-run",
      conversation_id: "new-travel",
      status: "succeeded",
    });
    act(() =>
      listener({
        run_id: "new-run",
        attempt: 1,
        seq: 2,
        type: "run.succeeded",
        payload: {},
      }),
    );
    await waitFor(() =>
      expect(screen.queryByText(/正在校验行程/)).not.toBeInTheDocument(),
    );
  });
  it("usesServerHeartbeatInterval", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const { client } = fixture();
    vi.mocked(client.registerPage).mockResolvedValue({
      page_id: "document",
      page_token: "token",
      expires_at: "",
      heartbeat_ms: 2000,
    });
    render(<App client={client} />);
    await waitFor(() => expect(client.registerPage).toHaveBeenCalledTimes(1));
    await screen.findByRole("button", { name: "通用聊天" });
    await act(async () => vi.advanceTimersByTime(2001));
    expect(client.heartbeat).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });
  it("ignoresDeltasFromPreviousAttempt", async () => {
    const { client, paused } = fixture();
    paused.status = "running";
    paused.attempt = 2;
    render(<App client={client} />);
    await userEvent.click(
      await screen.findByRole("button", { name: "暂停的对话" }),
    );
    await waitFor(() => expect(client.subscribe).toHaveBeenCalled());
    const listener = vi.mocked(client.subscribe).mock.calls[0][1];
    act(() =>
      listener({
        run_id: "run-old",
        attempt: 1,
        seq: 0,
        type: "message.delta",
        payload: { text: "late-attempt-secret" },
      }),
    );
    expect(screen.queryByText(/late-attempt-secret/)).not.toBeInTheDocument();
  });
  it("switchingAccountClearsPreviousWorkspace", async () => {
    const { client } = fixture();
    await openPaused(client);
    expect(screen.getByText("已保存的输出")).toBeInTheDocument();
    vi.mocked(client.me).mockResolvedValue({
      id: "second-user",
      username: "second-account",
    });
    vi.mocked(client.conversations).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", { key: "agent-platform.csrf" }),
      ),
    );
    await screen.findByText("second-account");
    await waitFor(() =>
      expect(screen.queryByText("已保存的输出")).not.toBeInTheDocument(),
    );
    expect(
      screen.queryByRole("button", { name: "继续任务" }),
    ).not.toBeInTheDocument();
  });
  it("reopensLastConversationWithoutStartingTask", async () => {
    localStorage.setItem("agent-platform.last-conversation.user", "old");
    const { client } = fixture();
    render(<App client={client} />);
    expect(
      await screen.findByRole("button", { name: "继续任务" }),
    ).toBeInTheDocument();
    expect(client.action).not.toHaveBeenCalled();
    expect(client.submit).not.toHaveBeenCalled();
  });
  it("closingSettingsDoesNotApplyUnsavedTheme", async () => {
    const { client } = fixture();
    render(<App client={client} />);
    await userEvent.click(await screen.findByRole("button", { name: "设置" }));
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "界面主题" }),
      "dark",
    );
    await userEvent.click(screen.getByRole("button", { name: "关闭" }));
    expect(document.documentElement.dataset.theme).toBe("system");
    expect(client.saveSettings).not.toHaveBeenCalled();
  });
  it("testSwitchEntryCreatesConversation", async () => {
    const { client } = fixture();
    render(<App client={client} />);
    await userEvent.click(
      await screen.findByRole("button", { name: "差旅助手" }),
    );
    expect(client.createConversation).toHaveBeenCalledWith("travel");
    await userEvent.click(screen.getByRole("button", { name: "通用聊天" }));
    expect(client.createConversation).toHaveBeenCalledWith("general");
  });
  it("testHistoryOpensOriginalAgent", async () => {
    const { client } = fixture();
    render(<App client={client} />);
    await userEvent.click(
      await screen.findByRole("button", { name: "差旅历史" }),
    );
    expect(await screen.findByText("差旅助手暂不可用")).toBeInTheDocument();
    expect(client.createConversation).not.toHaveBeenCalled();
  });
  it("testPausedOpenDoesNotResume", async () => {
    const { client } = fixture();
    await openPaused(client);
    expect(screen.getByText("已保存的输出")).toBeInTheDocument();
    expect(client.action).not.toHaveBeenCalled();
    expect(client.submit).not.toHaveBeenCalled();
  });
  it("testResumeRequiresClick", async () => {
    const { client } = fixture();
    await openPaused(client);
    await userEvent.click(screen.getByRole("button", { name: "继续任务" }));
    await waitFor(() =>
      expect(client.action).toHaveBeenCalledWith(
        "run-old",
        "resume",
        expect.objectContaining({ page_id: "new-document" }),
      ),
    );
  });
  it("testNewQuestionSupersedesPausedRun", async () => {
    const { client } = fixture();
    await openPaused(client);
    await userEvent.type(
      screen.getByRole("textbox", { name: "输入消息" }),
      "新的问题",
    );
    await userEvent.click(screen.getByRole("button", { name: "发送" }));
    await waitFor(() =>
      expect(client.submit).toHaveBeenCalledWith(
        "old",
        "新的问题",
        expect.objectContaining({ page_id: "new-document" }),
        expect.any(String),
      ),
    );
    expect(client.action).not.toHaveBeenCalled();
  });
  it("testPageSwitchKeepsHeartbeat", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const { client } = fixture();
    render(<App client={client} />);
    await waitFor(() => expect(client.registerPage).toHaveBeenCalledTimes(1));
    fireEvent.click(await screen.findByRole("button", { name: "差旅历史" }));
    await screen.findByText("差旅助手暂不可用");
    await act(async () => {
      vi.advanceTimersByTime(5001);
    });
    expect(client.heartbeat).toHaveBeenCalled();
    expect(client.leave).not.toHaveBeenCalled();
    vi.useRealTimers();
  });
  it("testTravelHasNoFakePlan", async () => {
    const { client } = fixture();
    render(<App client={client} />);
    await userEvent.click(
      await screen.findByRole("button", { name: "差旅历史" }),
    );
    expect(await screen.findByText("差旅助手暂不可用")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "输入消息" })).toBeDisabled();
    expect(client.submit).not.toHaveBeenCalled();
  });
  it("updatesPreviouslySavedPartialWithNewDeltas", async () => {
    const { client, paused } = fixture();
    paused.status = "running";
    vi.mocked(client.messages).mockResolvedValue({
      items: [
        {
          id: "partial",
          conversation_id: "old",
          run_id: "run-old",
          role: "assistant",
          content: "已保存的输出",
          ordinal: 1,
          tool_calls: [],
          tool_call_id: "",
          display_status: "partial",
        },
      ],
      next_after: 1,
    });
    render(<App client={client} />);
    await userEvent.click(
      await screen.findByRole("button", { name: "暂停的对话" }),
    );
    await waitFor(() => expect(client.subscribe).toHaveBeenCalled());
    const listener = vi.mocked(client.subscribe).mock.calls[0][1];
    act(() =>
      listener({
        run_id: "run-old",
        attempt: 1,
        seq: 0,
        type: "message.delta",
        payload: { text: "新的文字" },
      }),
    );
    expect(screen.getByText("已保存的输出新的文字")).toBeInTheDocument();
  });
});
