import { useMemo } from "react";
import type { ModelInfo, ModelProgress } from "../types";

const STATE_LABEL: Record<string, string> = {
  idle: "未启用",
  preparing: "准备中",
  loading: "加载中",
  ready: "已就绪",
  running: "识别中",
  error: "出错",
  unavailable: "本机不可用",
};

function statePill(model: ModelInfo): { cls: string; text: string } {
  const st = model.status;
  if (!st) return { cls: "", text: "未知" };
  const label = STATE_LABEL[st.state] ?? st.state;
  if (st.state === "ready" || st.state === "running") return { cls: "pill-ok", text: label };
  if (st.state === "error") return { cls: "pill-bad", text: label };
  if (st.state === "unavailable") return { cls: "pill-warn", text: label };
  if (st.state === "preparing" || st.state === "loading") return { cls: "pill-warn", text: label };
  return { cls: "", text: label };
}

function weightsText(model: ModelInfo): string | null {
  const st = model.status;
  if (!st) return null;
  if (model.weights === "none") return null;
  if (model.weights === "auto") {
    return st.weights_ready
      ? "引擎自管权重（已就绪）"
      : "引擎首次推理时自行下载权重";
  }
  if (st.weights_ready) {
    const mb = st.weights.bytes > 0 ? ` · ${(st.weights.bytes / 1024 / 1024).toFixed(1)} MB` : "";
    return `权重已下载${mb}`;
  }
  return "权重还没下载";
}

interface Props {
  model: ModelInfo;
  busy: string | null;
  // 这条模型当前的后台进度（没有就是 undefined）
  progress?: ModelProgress;
  onPrepare: (id: string, force: boolean) => void;
  onActivate: (id: string) => void;
  onDeactivate: (id: string) => void;
}

export default function ModelCard({ model, busy, progress, onPrepare, onActivate, onDeactivate }: Props) {
  const st = model.status;
  const pill = statePill(model);
  const weights = weightsText(model);
  const usable = st?.state !== "unavailable";
  const active = Boolean(st?.loaded) || st?.state === "ready" || st?.state === "running";
  // 依赖装上就能启用；只有 hub 权重还要等下载完（auto / none 由引擎自管）
  const canActivate =
    usable && Boolean(st?.extras_ready) && (model.weights !== "hub" || Boolean(st?.weights_ready));
  // 已经在后台跑了就别再让点：重复提交只会被后端 409 拒掉
  const working = st?.state === "preparing" || st?.state === "loading";
  const locked = busy !== null || working;

  const hint = useMemo(() => {
    if (!st) return "";
    if (st.state === "unavailable") return st.message;
    if (st.message && st.state !== "ready") return st.message;
    return "";
  }, [st]);

  // 本机不可用时逐条说清楚：没显卡 / 显存不够 / 内存不够 / 外部工具没装。
  // 旧版后端没 reasons 字段，回落到 status.message。
  const reasons = st?.state === "unavailable" ? (st.reasons ?? []) : [];

  return (
    <article className={`card ${active ? "card-active" : ""}`}>
      <div className="card-head">
        <div className="grow">
          <div className="row" style={{ gap: 6 }}>
            <span className="card-title">{model.name}</span>
            <span className={`pill ${pill.cls}`}>{pill.text}</span>
            {model.featured && <span className="chip">推荐</span>}
            {model.requires_gpu && (
              <span className={`chip ${st?.gpu_ok ? "" : ""}`} title="需要 NVIDIA GPU / Apple MPS">
                GPU
              </span>
            )}
          </div>
          <p className="muted small" style={{ margin: "4px 0 0" }}>
            {model.description}
          </p>
        </div>
        <span className="chip mono tiny">{model.id}</span>
      </div>

      <div className="row small muted" style={{ gap: 10 }}>
        <span>引擎 <code>{model.engine}</code></span>
        {model.languages.length > 0 && <span>语言 {model.languages.join(" / ")}</span>}
        {model.size_hint && <span>体积 {model.size_hint}</span>}
      </div>

      {model.tasks.length > 0 && (
        <div className="chips">
          {model.tasks.map((t) => (
            <span key={t} className="chip">
              {t}
            </span>
          ))}
        </div>
      )}

      {weights && <div className="small muted">{weights}</div>}
      {reasons.length > 0 ? (
        <ul className="small" style={{ margin: 0, paddingLeft: 18, color: "var(--warn)" }}>
          {reasons.map((reason, index) => (
            <li key={`${reason.kind}-${index}`}>{reason.message}</li>
          ))}
        </ul>
      ) : (
        hint && <div className="small" style={{ color: "var(--warn)" }}>{hint}</div>
      )}

      {working && (
        <div className="col" style={{ gap: 4 }}>
          <div className="progress">
            <span style={{ width: `${Math.round((progress?.percent ?? 0) * 100)}%` }} />
          </div>
          <span className="tiny muted">
            {progress?.note || (st?.state === "loading" ? "正在加载进内存…" : "正在后台下载 / 安装，可以继续用别的功能…")}
          </span>
        </div>
      )}

      {st?.repo && st.repo.reachable && (
        <div className="repo-line">
          <span className="mono">{st.repo.repo}</span>
          {st.repo.downloads > 0 && <span>↓ {st.repo.downloads.toLocaleString()}</span>}
          {st.repo.likes > 0 && <span>♥ {st.repo.likes.toLocaleString()}</span>}
          {st.repo.total_bytes > 0 && (
            <span>{(st.repo.total_bytes / 1024 / 1024 / 1024).toFixed(2)} GB</span>
          )}
          {st.repo.gated && <span style={{ color: "var(--warn)" }}>需同意条款</span>}
        </div>
      )}
      {st?.repo?.error && <div className="tiny" style={{ color: "var(--warn)" }}>{st.repo.error}</div>}

      <div className="row">
        {model.weights === "hub" && (
          <button
            className="btn small"
            disabled={locked || !usable}
            onClick={() => onPrepare(model.id, false)}
          >
            {working ? "准备中…" : st?.weights_ready ? "重新下载权重" : "下载权重"}
          </button>
        )}
        {model.weights === "auto" && model.engine !== "tesseract" && !st?.extras_ready && (
          <button
            className="btn small primary"
            disabled={locked || !usable}
            onClick={() => onPrepare(model.id, false)}
          >
            {working ? "准备中…" : model.engine === "external" ? "安装命令" : "安装依赖"}
          </button>
        )}
        {active ? (
          <button className="btn small danger" disabled={locked} onClick={() => onDeactivate(model.id)}>
            卸载
          </button>
        ) : canActivate ? (
          <button
            className="btn small primary"
            disabled={locked}
            onClick={() => onActivate(model.id)}
          >
            启用
          </button>
        ) : null}
        {model.homepage && (
          <a className="btn small ghost" href={model.homepage} target="_blank" rel="noreferrer">
            官网
          </a>
        )}
        <span className="grow" />
        {st && st.workers > 0 && <span className="tiny muted">worker × {st.workers}</span>}
      </div>
    </article>
  );
}
