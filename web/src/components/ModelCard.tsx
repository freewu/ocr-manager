import { useMemo } from "react";
import type { ModelInfo } from "../types";

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
  onPrepare: (id: string, force: boolean) => void;
  onActivate: (id: string) => void;
  onDeactivate: (id: string) => void;
}

export default function ModelCard({ model, busy, onPrepare, onActivate, onDeactivate }: Props) {
  const st = model.status;
  const pill = statePill(model);
  const weights = weightsText(model);
  const usable = st?.state !== "unavailable";
  const active = Boolean(st?.loaded) || st?.state === "ready" || st?.state === "running";
  // 权重和依赖都到位才给「启用」：不然点了只会报「先下载」，不如直接显示下载。
  const canActivate = usable && Boolean(st?.weights_ready) && Boolean(st?.extras_ready);

  const hint = useMemo(() => {
    if (!st) return "";
    if (st.state === "unavailable") return st.message;
    if (st.message && st.state !== "ready") return st.message;
    return "";
  }, [st]);

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
      {hint && <div className="small" style={{ color: "var(--warn)" }}>{hint}</div>}

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
            disabled={busy !== null || !usable}
            onClick={() => onPrepare(model.id, false)}
          >
            {st?.weights_ready ? "重新下载权重" : "下载权重"}
          </button>
        )}
        {model.weights === "auto" && model.engine !== "tesseract" && !st?.extras_ready && (
          <button
            className="btn small primary"
            disabled={busy !== null || !usable}
            onClick={() => onPrepare(model.id, false)}
          >
            安装依赖
          </button>
        )}
        {active ? (
          <button className="btn small danger" disabled={busy !== null} onClick={() => onDeactivate(model.id)}>
            卸载
          </button>
        ) : canActivate ? (
          <button
            className="btn small primary"
            disabled={busy !== null}
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
