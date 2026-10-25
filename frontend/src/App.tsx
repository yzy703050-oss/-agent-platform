import {
  useState,
  useEffect,
  useRef,
  useCallback,
  type FormEvent,
} from "react";
import {
  api,
  allMessages,
  allConversations,
  errorText,
  APIError,
  type PlatformAPI,
  type User,
  type Conversation,
  type Run,
  type Message,
  type AgentInfo,
  type AgentType,
  type Settings,
  type Action,
  type RunEvent,
} from "./api/client";
import { usePageLease } from "./hooks/usePageLease";
import { useRunEvents } from "./hooks/useRunEvents";
import { AuthForm } from "./components/AuthForm";
import { ConversationSidebar } from "./components/ConversationSidebar";
import { AgentPicker } from "./components/AgentPicker";
import { TravelResult } from "./components/TravelResult";
import { WebSearchResult } from "./components/WebSearchResult";
import { RunControls, activeStatuses } from "./components/RunControls";
import "./styles.css";

export default function App({ client = api }: { client?: PlatformAPI }) {
  const [user, setUser] = useState<User | null>(null),
    [booting, setBooting] = useState(true),
    [error, setError] = useState("");
  const [history, setHistory] = useState<Conversation[]>([]),
    [selected, setSelected] = useState<Conversation | null>(null),
    [messages, setMessages] = useState<Message[]>([]),
    [run, setRun] = useState<Run | null>(null);
  const [workspaceOwner, setWorkspaceOwner] = useState<string | null>(null);
  const [agents, setAgents] = useState<AgentInfo[]>([]),
    [settings, setSettings] = useState<Settings>({
      theme: "system",
      reply_preference: "",
    }),
    [settingsOpen, setSettingsOpen] = useState(false),
    [rename, setRename] = useState<Conversation | null>(null),
    [title, setTitle] = useState("");
  const [settingsDraft, setSettingsDraft] = useState<Settings>({
    theme: "system",
    reply_preference: "",
  });
  const [text, setText] = useState(""),
    [busy, setBusy] = useState(false),
    [loading, setLoading] = useState(false),
    [live, setLive] = useState(""),
    [toolState, setToolState] = useState("");
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  const runRef = useRef(run);
  runRef.current = run;
  const pending = useRef<{ text: string; key: string } | null>(null);
  const fail = useCallback((e: unknown) => {
    setError(errorText(e));
    if (e instanceof APIError && e.code === "unauthenticated") setUser(null);
  }, []);
  const page = usePageLease(client, user?.id, fail);
  useEffect(() => {
    let alive = true;
    void client
      .me()
      .then((u) => {
        if (alive) setUser(u);
      })
      .catch((e) => {
        if (!(e instanceof APIError && e.code === "unauthenticated") && alive)
          fail(e);
      })
      .finally(() => {
        if (alive) setBooting(false);
      });
    return () => {
      alive = false;
    };
  }, [client, fail]);
  useEffect(() => {
    if (!user) {
      setHistory([]);
      setSelected(null);
      setRun(null);
      setMessages([]);
      return;
    }
    let alive = true;
    setError("");
    setWorkspaceOwner(user.id);
    setSelected(null);
    setHistory([]);
    setMessages([]);
    setRun(null);
    setLive("");
    setToolState("");
    void Promise.all([
      allConversations(client),
      client.agents(),
      client.settings(),
    ])
      .then(([h, a, s]) => {
        if (alive) {
          setHistory(h);
          setAgents(a.items);
          setSettings(s);
          const remembered = localStorage.getItem(
            "agent-platform.last-conversation." + user.id,
          );
          const restored = h.find((c) => c.id === remembered);
          if (restored && !selectedRef.current) setSelected(restored);
        }
      })
      .catch(fail);
    const check = () => {
      void client
        .me()
        .then((u) => {
          if (alive) setUser(u);
        })
        .catch(fail);
    };
    const timer = setInterval(check, 20000);
    const storage = (e: StorageEvent) => {
      if (e.key === "agent-platform.csrf") check();
    };
    window.addEventListener("storage", storage);
    return () => {
      alive = false;
      clearInterval(timer);
      window.removeEventListener("storage", storage);
    };
  }, [client, user?.id, fail]);
  useEffect(() => {
    document.documentElement.dataset.theme = settings.theme;
  }, [settings.theme]);
  useEffect(() => {
    if (user && workspaceOwner === user.id && selected)
      localStorage.setItem(
        "agent-platform.last-conversation." + user.id,
        selected.id,
      );
  }, [user?.id, selected?.id, workspaceOwner]);
  useEffect(() => {
    if (!selected) {
      setMessages([]);
      setRun(null);
      return;
    }
    let alive = true;
    setLoading(true);
    setRun(null);
    setMessages([]);
    setLive("");
    setToolState("");
    setText("");
    pending.current = null;
    void allMessages(client, selected.id)
      .then(async (rows) => {
        const latest = rows[rows.length - 1]?.run_id;
        const state = latest ? await client.run(latest) : null;
        if (alive) {
          setMessages(rows);
          setRun(state);
          setLive(state?.partial_output || "");
        }
      })
      .catch((e) => {
        if (alive) fail(e);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [client, selected?.id, fail]);
  const refresh = useCallback(async () => {
    const current = runRef.current;
    if (!current) return;
    try {
      const [state, rows] = await Promise.all([
        client.run(current.id),
        allMessages(client, current.conversation_id),
      ]);
      if (
        selectedRef.current?.id !== current.conversation_id ||
        runRef.current?.id !== current.id
      )
        return;
      setRun(state);
      setMessages(rows);
      setLive(state.partial_output || "");
    } catch (e) {
      fail(e);
    }
  }, [client, fail]);
  const onEvent = useCallback(
    (event: RunEvent) => {
      if (
        event.run_id !== runRef.current?.id ||
        event.attempt !== runRef.current?.attempt
      )
        return;
      if (event.type === "message.delta")
        setLive((old) => (old + (event.payload.text || "")).slice(0, 256000));
      else if (event.type.startsWith("tool."))
        setToolState(
          event.type === "tool.started"
            ? event.payload.name === "web_search"
              ? "正在联网搜索…"
              : event.payload.name === "get_my_settings"
                ? "正在读取个人设置…"
                : "正在执行差旅工具…"
            : event.type === "tool.completed"
              ? event.payload.name === "web_search"
                ? "联网搜索已返回结果"
                : event.payload.name === "get_my_settings"
                  ? "已读取个人设置"
                  : "差旅工具已返回结果"
              : event.payload.name === "web_search"
                ? "联网搜索失败"
                : "工具执行失败",
        );
      else if (event.type === "travel.phase") {
        const labels: Record<string, string> = {
          planning: "正在规划行程…",
          querying: "正在查询差旅信息…",
          validating: "正在校验行程…",
          reply: "正在整理已确认的结果…",
        };
        setToolState(labels[event.payload.phase || ""] || "正在处理差旅任务…");
      } else if (event.type.startsWith("run.")) void refresh();
    },
    [refresh],
  );
  useRunEvents(
    client,
    run?.id,
    !!run && activeStatuses.has(run.status),
    onEvent,
    refresh,
  );
  async function choose(agent: AgentType) {
    setBusy(true);
    setError("");
    try {
      const c = await client.createConversation(agent);
      setHistory((h) => [...h, c]);
      setSelected(c);
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }
  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!selected || !page || !text.trim() || busy) return;
    const c = selected;
    const content = text.trim();
    if (pending.current?.text !== content)
      pending.current = { text: content, key: crypto.randomUUID() };
    setBusy(true);
    setError("");
    try {
      const accepted = await client.submit(
        c.id,
        content,
        page,
        pending.current.key,
      );
      if (selectedRef.current?.id !== c.id) return;
      pending.current = null;
      setText("");
      setRun(accepted);
      setLive(accepted.partial_output || "");
      setToolState("");
      setMessages(await allMessages(client, c.id));
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }
  async function action(action: Action) {
    if (!run) return;
    setBusy(true);
    setError("");
    const id = run.id;
    try {
      const state = await client.action(
        id,
        action,
        action === "resume" && page ? page : undefined,
      );
      if (runRef.current?.id === id) {
        setRun(state);
        setLive(state.partial_output || "");
        await refresh();
      }
    } catch (e) {
      fail(e);
      await refresh();
    } finally {
      setBusy(false);
    }
  }
  async function logout() {
    try {
      await client.logout();
      setUser(null);
      setError("");
    } catch (e) {
      fail(e);
    }
  }
  async function saveSettings(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      setSettings(await client.saveSettings(settingsDraft));
      setSettingsOpen(false);
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }
  async function saveTitle(e: FormEvent) {
    e.preventDefault();
    if (!rename) return;
    setBusy(true);
    try {
      await client.renameConversation(rename.id, title);
      setHistory((h) =>
        h.map((c) => (c.id === rename.id ? { ...c, title } : c)),
      );
      if (selected?.id === rename.id) setSelected({ ...selected, title });
      setRename(null);
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }
  async function deleteConversation(c: Conversation) {
    if (!window.confirm("删除会话“" + c.title + "”？")) return;
    try {
      await client.deleteConversation(c.id);
      setHistory((h) => h.filter((item) => item.id !== c.id));
      if (selected?.id === c.id) setSelected(null);
    } catch (e) {
      fail(e);
    }
  }
  if (booting || (user && workspaceOwner !== user.id))
    return (
      <main className="boot" role="status">
        正在打开工作台…
      </main>
    );
  if (!user)
    return (
      <>
        <AuthForm client={client} onLogin={setUser} />
        {error && (
          <div className="global-error" role="alert">
            {error}
          </div>
        )}
      </>
    );
  const travel = selected?.agent_type === "travel",
    modelReady = agents.some(
      (a) => a.id === (selected?.agent_type || "general") && a.available,
    );
  const running = !!run && activeStatuses.has(run.status);
  const partialInHistory = messages.some(
    (m) => m.run_id === run?.id && m.display_status === "partial",
  );
  const toolNames = new Map<string, string>();
  for (const message of messages) {
    if (message.role === "assistant") {
      for (const call of message.tool_calls || []) {
        toolNames.set(`${message.run_id}:${call.id}`, call.name);
      }
    }
  }
  return (
    <div className="app-shell">
      <ConversationSidebar
        items={history}
        selected={selected?.id}
        onSelect={setSelected}
        onRename={(c) => {
          setRename(c);
          setTitle(c.title);
        }}
        onDelete={(c) => void deleteConversation(c)}
      />
      <main className="main-panel">
        <header className="topbar">
          <div>
            <span className="eyebrow">AGENT WORKSPACE</span>
            <h1>{selected?.title || "你的 AI 工作台"}</h1>
          </div>
          <div className="account">
            <span className="avatar">{user.username[0].toUpperCase()}</span>
            <span>{user.username}</span>
            <button
              onClick={() => {
                setSettingsDraft(settings);
                setSettingsOpen(true);
              }}
            >
              设置
            </button>
            <button className="text-button" onClick={() => void logout()}>
              退出
            </button>
          </div>
        </header>
        <section className="workspace">
          <div className="workspace-heading">
            <div>
              <h2>{selected ? "当前助手" : "从一次对话开始"}</h2>
              <p>选择入口会新建会话，历史任务保存在左侧。</p>
            </div>
            <span className="local-label">
              <i />
              独立任务 · 持久保存
            </span>
          </div>
          <AgentPicker
            selected={selected?.agent_type}
            busy={busy}
            travelReady={agents.some((a) => a.id === "travel" && a.available)}
            onSelect={(a) => void choose(a)}
          />
          {error && (
            <div className="notice error" role="alert">
              {error}
              <button aria-label="关闭错误" onClick={() => setError("")}>
                ×
              </button>
            </div>
          )}
          <div className="chat-panel">
            <div className="chat-heading">
              <span>{travel ? "差旅助手" : "通用聊天"}</span>
              <small>{selected ? "会话已保存" : "选择助手，创建新对话"}</small>
            </div>
            <RunControls
              run={run}
              busy={busy}
              hasPage={!!page}
              onAction={(a) => void action(a)}
            />
            <div
              className="message-list"
              aria-live="polite"
              aria-busy={loading}
            >
              {loading ? (
                <p className="muted">正在加载历史…</p>
              ) : travel && !modelReady && messages.length === 0 ? (
                <div className="empty-state">
                  <div className="empty-icon">↗</div>
                  <h2>差旅助手暂不可用</h2>
                  <p>请联系管理员检查模型配置。已有历史仍可查看。</p>
                </div>
              ) : messages.length === 0 && !run ? (
                <div className="empty-state">
                  <div className="empty-icon">✳</div>
                  <h2>
                    {travel ? "开始规划一次差旅" : "今天，想一起做点什么？"}
                  </h2>
                  <p>
                    {travel
                      ? "提供出发地、目的地、日期和预算。缺少的条件会继续向你确认。"
                      : "可以聊一个问题，也可以让我读取你的回复偏好。"}
                  </p>
                  <div className="suggestion">
                    {travel
                      ? "试试：10月25日从上海去杭州，单程，当天返回不住宿。"
                      : "试试：“查看我的主题和回复偏好”"}
                  </div>
                </div>
              ) : (
                <>
                  {messages.map((m) => (
                    <article key={m.id} className={"message " + m.role}>
                      <div className="message-label">
                        {m.role === "user"
                          ? "你"
                          : m.role === "tool"
                            ? "工具结果"
                            : "小豆包"}
                        {m.display_status === "partial" && (
                          <small>已保存的部分输出</small>
                        )}
                      </div>
                      {m.content &&
                        (m.role === "tool" &&
                        toolNames.get(`${m.run_id}:${m.tool_call_id}`) ===
                          "web_search" ? (
                          <WebSearchResult content={m.content} />
                        ) : travel && m.role === "tool" ? (
                          <TravelResult content={m.content} />
                        ) : (
                          <p>
                            {m.display_status === "partial" &&
                            m.run_id === run?.id &&
                            running
                              ? live || m.content
                              : m.content}
                          </p>
                        ))}
                      {m.tool_calls?.map((t) => (
                        <details key={t.id}>
                          <summary>调用 {t.name}</summary>
                          <pre>{t.arguments}</pre>
                        </details>
                      ))}
                    </article>
                  ))}
                  {live && running && !partialInHistory && (
                    <article className="message assistant streaming">
                      <div className="message-label">小豆包 · 正在回复</div>
                      <p>{live}</p>
                    </article>
                  )}
                </>
              )}
              {toolState && run && activeStatuses.has(run.status) && (
                <p className="tool-status">◈ {toolState}</p>
              )}
            </div>
            {run?.status === "paused" && (
              <div className="notice pause-note">
                可以继续原任务；直接发送新问题会结束原任务，旧记录仍可查看，并从新任务上下文中排除。
              </div>
            )}
            {run?.status === "interrupted" && (
              <div className="notice">
                没有可用的恢复断点，可以发送新问题开始新的任务。
              </div>
            )}
            {run?.error_code && run.status !== "cancelled" && (
              <div className="run-error">
                原因：
                {run.error_code === "travel_evidence_expired"
                  ? "已有候选依据已过期，请重新查询并选择；历史行程仍可查看。"
                  : run.error_code === "travel_result_unknown"
                    ? "操作结果待核实，已停止自动重放，请联系管理员核对保存记录。"
                    : run.error_code}
              </div>
            )}
            {!modelReady && (
              <div className="notice">
                {travel ? "差旅助手" : "聊天"}暂不可用，请联系管理员配置模型。
              </div>
            )}
            <form className="composer" onSubmit={submit}>
              <textarea
                aria-label="输入消息"
                placeholder={
                  !selected
                    ? "先选择上方的助手"
                    : run?.status === "paused"
                      ? "输入新问题，开始新任务…"
                      : "输入你的问题…"
                }
                value={text}
                onChange={(e) => setText(e.target.value)}
                disabled={!selected || running || !modelReady}
                maxLength={16000}
              />
              <div className="composer-footer">
                <small>
                  {!page
                    ? "正在建立页面连接…"
                    : running
                      ? "任务执行中，可切换查看其他会话"
                      : "关闭页面后请求安全暂停；重开后手动继续。"}
                </small>
                <button
                  aria-label="发送"
                  className="primary"
                  disabled={
                    busy ||
                    loading ||
                    !page ||
                    !selected ||
                    running ||
                    !modelReady ||
                    !text.trim()
                  }
                >
                  发送 <span>↗</span>
                </button>
              </div>
            </form>
          </div>
          <footer className="workspace-footer">
            AI 内容仅供参考 · 任务可恢复状态以实际保存的断点为准
          </footer>
        </section>
      </main>
      {settingsOpen && (
        <div className="modal-backdrop">
          <form
            className="modal"
            onSubmit={saveSettings}
            role="dialog"
            aria-modal="true"
            aria-label="个人设置"
          >
            <h2>个人设置</h2>
            <label>
              界面主题
              <select
                value={settingsDraft.theme}
                onChange={(e) =>
                  setSettingsDraft({
                    ...settingsDraft,
                    theme: e.target.value as Settings["theme"],
                  })
                }
              >
                <option value="system">跟随系统</option>
                <option value="light">浅色</option>
                <option value="dark">深色</option>
              </select>
            </label>
            <label>
              回复偏好
              <textarea
                maxLength={2000}
                value={settingsDraft.reply_preference}
                onChange={(e) =>
                  setSettingsDraft({
                    ...settingsDraft,
                    reply_preference: e.target.value,
                  })
                }
              />
            </label>
            <p className="muted">聊天助手可通过只读工具读取这些设置。</p>
            <div className="modal-actions">
              <button type="button" onClick={() => setSettingsOpen(false)}>
                关闭
              </button>
              <button className="primary" disabled={busy}>
                保存设置
              </button>
            </div>
          </form>
        </div>
      )}
      {rename && (
        <div className="modal-backdrop">
          <form
            className="modal"
            onSubmit={saveTitle}
            role="dialog"
            aria-modal="true"
            aria-label="重命名会话"
          >
            <h2>重命名会话</h2>
            <label>
              标题
              <input
                value={title}
                maxLength={100}
                required
                onChange={(e) => setTitle(e.target.value)}
              />
            </label>
            <div className="modal-actions">
              <button type="button" onClick={() => setRename(null)}>
                取消
              </button>
              <button className="primary" disabled={busy}>
                保存
              </button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}
