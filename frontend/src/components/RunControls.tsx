import type { Action, Run } from "../api/client";
export const activeStatuses = new Set([
  "accepted",
  "running",
  "pausing",
  "resuming",
  "cancelling",
]);
export const statusText: Record<string, string> = {
  accepted: "等待执行",
  running: "执行中",
  pausing: "正在保存断点",
  paused: "任务已暂停",
  resuming: "恢复中",
  cancelling: "正在结束",
  cancelled: "任务已结束",
  succeeded: "已完成",
  failed: "执行失败",
  timed_out: "执行超时",
  interrupted: "任务已中断",
};
export function RunControls({
  run,
  busy,
  hasPage,
  onAction,
}: {
  run: Run | null;
  busy: boolean;
  hasPage: boolean;
  onAction: (a: Action) => void;
}) {
  if (!run) return null;
  return (
    <div className="run-controls">
      <span className={"status " + run.status}>
        <i />
        {statusText[run.status] || run.status}
      </span>
      <small>第 {run.attempt} 次执行</small>
      <div className="run-actions">
        {run.status === "paused" && (
          <button
            className="primary compact"
            disabled={busy || !hasPage}
            onClick={() => onAction("resume")}
          >
            继续任务
          </button>
        )}
        {run.status === "running" && (
          <button disabled={busy} onClick={() => onAction("pause")}>
            暂停
          </button>
        )}
        {(activeStatuses.has(run.status) || run.status === "paused") && (
          <button
            className="danger-text"
            disabled={busy || run.status === "cancelling"}
            onClick={() => onAction("cancel")}
          >
            结束任务
          </button>
        )}
      </div>
    </div>
  );
}
