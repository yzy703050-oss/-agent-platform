import type { AgentType } from "../api/client";
export function AgentPicker({
  selected,
  busy,
  onSelect,
}: {
  selected?: AgentType;
  busy: boolean;
  onSelect: (a: AgentType) => void;
}) {
  return (
    <div className="agent-picker" aria-label="选择助手">
      <button
        disabled={busy}
        className={selected === "general" ? "chosen" : ""}
        aria-label="通用聊天"
        onClick={() => onSelect("general")}
      >
        <span>✳</span>通用聊天<small>日常问答与工具</small>
      </button>
      <button
        disabled={busy}
        className={selected === "travel" ? "chosen" : ""}
        aria-label="差旅助手"
        onClick={() => onSelect("travel")}
      >
        <span>↗</span>差旅助手<small>即将接入</small>
      </button>
    </div>
  );
}
