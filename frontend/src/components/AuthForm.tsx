import { useState, type FormEvent } from "react";
import { errorText, type PlatformAPI, type User } from "../api/client";
export function AuthForm({
  client,
  onLogin,
}: {
  client: PlatformAPI;
  onLogin: (u: User) => void;
}) {
  const [register, setRegister] = useState(false),
    [username, setUsername] = useState(""),
    [password, setPassword] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (register) await client.register(username, password);
      const out = await client.login(username, password);
      setPassword("");
      onLogin(out.user);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="auth-page">
      <div className="auth-intro">
        <div className="brand-mark">豆</div>
        <span className="eyebrow">YOUR AGENT WORKSPACE</span>
        <h1>
          一个工作台，
          <br />
          连接你的 AI 助手。
        </h1>
        <p>
          对话、工具与任务进度，保存在同一个地方。
          <br />
          离开后，再从可用的断点继续。
        </p>
        <div className="intro-note">
          <i />
          通用聊天 · 差旅助手预留中
        </div>
      </div>
      <form className="auth-card" onSubmit={submit}>
        <span className="eyebrow">小豆包 · 工作台</span>
        <h2>{register ? "创建你的账户" : "欢迎回来"}</h2>
        <p>登录后查看会话与任务进度</p>
        <label>
          用户名
          <input
            autoComplete="username"
            minLength={3}
            maxLength={100}
            required
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </label>
        <label>
          密码
          <input
            type="password"
            autoComplete={register ? "new-password" : "current-password"}
            minLength={8}
            maxLength={72}
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <button className="primary" disabled={busy}>
          {busy ? "正在处理…" : register ? "注册并登录" : "登录"}
        </button>
        <button
          className="text-button"
          type="button"
          onClick={() => {
            setRegister(!register);
            setError("");
          }}
        >
          {register ? "已有账户？登录" : "还没有账户？注册"}
        </button>
      </form>
    </main>
  );
}
