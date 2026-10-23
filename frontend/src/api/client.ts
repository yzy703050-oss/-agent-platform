import { readEvents } from "./events";
export type AgentType = "general" | "travel";
export interface User {
  id: string;
  username: string;
}
export interface Conversation {
  id: string;
  agent_type: AgentType;
  title: string;
  created_at: string;
}
export interface ToolCall {
  id: string;
  name: string;
  arguments: string;
}
export interface Message {
  id: string;
  conversation_id: string;
  run_id: string;
  role: string;
  content: string;
  tool_call_id: string;
  tool_calls: ToolCall[];
  ordinal: number;
  display_status?: string;
}
export interface Run {
  id: string;
  conversation_id: string;
  page_id: string;
  status: string;
  attempt: number;
  version: number;
  context_eligible: boolean;
  error_code: string;
  partial_output: string;
}
export interface PageLease {
  page_id: string;
  page_token: string;
  expires_at: string;
}
export interface Settings {
  theme: "system" | "light" | "dark";
  reply_preference: string;
}
export interface AgentInfo {
  id: AgentType;
  available: boolean;
  reason?: string;
}
export interface RunEvent {
  run_id: string;
  type: string;
  attempt: number;
  seq: number;
  payload: { text?: string; name?: string; duration_ms?: number };
}
export type Action = "pause" | "resume" | "cancel";
export interface PlatformAPI {
  me(): Promise<User>;
  register(username: string, password: string): Promise<User>;
  login(
    username: string,
    password: string,
  ): Promise<{ user: User; csrf_token: string }>;
  logout(): Promise<void>;
  settings(): Promise<Settings>;
  saveSettings(s: Settings): Promise<Settings>;
  agents(): Promise<{ items: AgentInfo[] }>;
  conversations(
    cursor?: string,
  ): Promise<{ items: Conversation[]; next_cursor: string }>;
  createConversation(agent: AgentType): Promise<Conversation>;
  renameConversation(id: string, title: string): Promise<void>;
  deleteConversation(id: string): Promise<void>;
  messages(
    id: string,
    after?: number,
  ): Promise<{ items: Message[]; next_after: number }>;
  registerPage(): Promise<PageLease>;
  heartbeat(page: PageLease): Promise<void>;
  leave(page: PageLease): Promise<void>;
  submit(id: string, text: string, page: PageLease, key: string): Promise<Run>;
  run(id: string): Promise<Run>;
  action(id: string, action: Action, page?: PageLease): Promise<Run>;
  subscribe(
    id: string,
    onEvent: (e: RunEvent) => void,
    signal: AbortSignal,
  ): Promise<void>;
}
export class APIError extends Error {
  constructor(
    public code: string,
    public requestID = "",
  ) {
    super(code);
  }
}
const csrfKey = "agent-platform.csrf";
async function request<T>(
  path: string,
  method = "GET",
  body?: unknown,
  extra: Record<string, string> = {},
  keepalive = false,
): Promise<T> {
  const headers: Record<string, string> = { ...extra };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET") {
    const csrf = localStorage.getItem(csrfKey);
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }
  const response = await fetch("/api/v1" + path, {
    method,
    headers,
    credentials: "same-origin",
    cache: "no-store",
    body: body === undefined ? undefined : JSON.stringify(body),
    keepalive,
  });
  if (!response.ok) {
    let error: { code?: string; request_id?: string } = {};
    try {
      error = await response.json();
    } catch {
      /* A proxy may return a non-JSON failure. */
    }
    throw new APIError(error.code || "network_error", error.request_id);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}
const pageHeaders = (p: PageLease) => ({ "X-Page-Token": p.page_token });
export const api: PlatformAPI = {
  me: () => request("/me"),
  register: (username, password) =>
    request("/auth/register", "POST", { username, password }),
  login: async (username, password) => {
    const out = await request<{ user: User; csrf_token: string }>(
      "/auth/login",
      "POST",
      { username, password },
    );
    localStorage.setItem(csrfKey, out.csrf_token);
    return out;
  },
  logout: async () => {
    await request("/auth/logout", "POST", {});
    localStorage.removeItem(csrfKey);
  },
  settings: () => request("/settings"),
  saveSettings: (s) => request("/settings", "PATCH", s),
  agents: () => request("/agents"),
  conversations: (cursor = "") =>
    request("/conversations?limit=100&cursor=" + encodeURIComponent(cursor)),
  createConversation: (agent) =>
    request("/conversations", "POST", { agent_type: agent }),
  renameConversation: (id, title) =>
    request("/conversations/" + encodeURIComponent(id), "PATCH", { title }),
  deleteConversation: (id) =>
    request("/conversations/" + encodeURIComponent(id), "DELETE"),
  messages: (id, after = 0) =>
    request(
      "/conversations/" +
        encodeURIComponent(id) +
        "/messages?limit=100&after=" +
        after,
    ),
  registerPage: () => request("/pages", "POST", {}),
  heartbeat: (p) =>
    request(
      "/pages/" + encodeURIComponent(p.page_id) + "/heartbeat",
      "POST",
      {},
      pageHeaders(p),
    ),
  leave: (p) =>
    request(
      "/pages/" + encodeURIComponent(p.page_id) + "/leave",
      "POST",
      {},
      pageHeaders(p),
      true,
    ),
  submit: (id, text, p, key) =>
    request(
      "/conversations/" + encodeURIComponent(id) + "/runs",
      "POST",
      { content: text, page_id: p.page_id },
      { ...pageHeaders(p), "Idempotency-Key": key },
    ),
  run: (id) => request("/runs/" + encodeURIComponent(id)),
  action: (id, action, p) =>
    request(
      "/runs/" + encodeURIComponent(id) + "/" + action,
      "POST",
      p ? { page_id: p.page_id } : {},
      p ? pageHeaders(p) : {},
    ),
  subscribe: async (id, onEvent, signal) => {
    const response = await fetch(
      "/api/v1/runs/" + encodeURIComponent(id) + "/events",
      {
        credentials: "same-origin",
        cache: "no-store",
        signal,
        headers: { Accept: "text/event-stream" },
      },
    );
    if (!response.ok)
      throw new APIError(
        response.status === 401 ? "unauthenticated" : "stream_unavailable",
      );
    await readEvents(response, onEvent, signal);
  },
};
export function errorText(error: unknown): string {
  const labels: Record<string, string> = {
    unauthenticated: "登录已失效，请重新登录。",
    invalid_input: "请检查输入内容。",
    conflict: "当前任务状态已变化，请刷新查看。",
    idempotency_conflict: "这次请求与之前的内容不一致，请重新发送。",
    run_quota_exceeded: "同时执行的任务已达上限，请稍后再试。",
    model_unavailable: "聊天服务暂不可用，请联系管理员配置模型。",
    agent_unavailable: "该助手尚未接入。",
    checkpoint_incompatible: "原断点与当前配置不兼容，无法继续。",
    not_found: "资源不存在、页面已过期或没有访问权限。",
    csrf_rejected: "登录信息已更新，请重新登录。",
  };
  if (error instanceof APIError)
    return (
      (labels[error.code] || "请求失败，请稍后重试。") +
      (error.requestID ? " 请求编号：" + error.requestID : "")
    );
  return "连接失败，请检查网络后重试。";
}
export async function allMessages(
  client: PlatformAPI,
  id: string,
): Promise<Message[]> {
  const all: Message[] = [];
  let after = 0;
  for (;;) {
    const page = await client.messages(id, after);
    all.push(...page.items);
    if (page.items.length < 100 || page.next_after <= after) return all;
    after = page.next_after;
  }
}
export async function allConversations(
  client: PlatformAPI,
): Promise<Conversation[]> {
  const all: Conversation[] = [];
  let cursor = "";
  for (;;) {
    const page = await client.conversations(cursor);
    all.push(...page.items);
    if (!page.next_cursor || page.next_cursor === cursor) return all;
    cursor = page.next_cursor;
  }
}
