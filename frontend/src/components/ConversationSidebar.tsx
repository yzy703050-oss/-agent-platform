import type { Conversation } from "../api/client";
export function ConversationSidebar({
  items,
  selected,
  onSelect,
  onRename,
  onDelete,
}: {
  items: Conversation[];
  selected?: string;
  onSelect: (c: Conversation) => void;
  onRename: (c: Conversation) => void;
  onDelete: (c: Conversation) => void;
}) {
  return (
    <aside className="sidebar">
      <div className="brand">
        <div className="brand-mark small">豆</div>
        <div>
          <strong>小豆包</strong>
          <small>Agent 工作台</small>
        </div>
      </div>
      <div className="sidebar-caption">
        会话历史 <span>{items.length}</span>
      </div>
      <nav aria-label="会话历史" className="history-list">
        {[...items].reverse().map((c) => (
          <div
            key={c.id}
            className={"history-row " + (selected === c.id ? "selected" : "")}
          >
            <button
              className="history-open"
              aria-label={c.title}
              onClick={() => onSelect(c)}
              aria-current={selected === c.id ? "page" : undefined}
            >
              <span className="history-symbol">
                {c.agent_type === "travel" ? "↗" : "◌"}
              </span>
              <span>
                <strong>{c.title}</strong>
                <small>
                  {c.agent_type === "travel" ? "差旅助手" : "通用聊天"}
                </small>
              </span>
            </button>
            <div className="row-actions">
              <button
                aria-label={"重命名 " + c.title}
                onClick={() => onRename(c)}
              >
                ✎
              </button>
              <button
                aria-label={"删除 " + c.title}
                onClick={() => onDelete(c)}
              >
                ×
              </button>
            </div>
          </div>
        ))}
        {items.length === 0 && (
          <p className="empty-history">
            还没有会话
            <br />
            从右侧选择一个助手开始
          </p>
        )}
      </nav>
      <div className="sidebar-footer">
        <i />
        历史与进度自动保存
      </div>
    </aside>
  );
}
