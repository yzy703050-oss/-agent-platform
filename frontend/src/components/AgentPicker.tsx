import type { AgentType } from "../api/client";
export function AgentPicker({
  selected,
  busy,
  travelReady,
  onSelect,
}: {
  selected?: AgentType;
  busy: boolean;
  travelReady: boolean;
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
        <span>↗</span>差旅助手
        <small>{travelReady ? "行程规划与真实查询" : "暂不可用"}</small>
      </button>
    </div>
  );
}
