import { useMemo, useState } from "react";
import type { GpuInfo, ModelInfo } from "../types";
import ModelCard from "./ModelCard";

type Filter = "all" | "ready" | "missing" | "gpu" | "external";

const FILTERS: { id: Filter; label: string }[] = [
  { id: "all", label: "全部" },
  { id: "ready", label: "可直接用" },
  { id: "missing", label: "缺依赖/权重" },
  { id: "gpu", label: "需要 GPU" },
  { id: "external", label: "外部登记" },
];

function match(model: ModelInfo, filter: Filter): boolean {
  const st = model.status;
  switch (filter) {
    case "ready":
      return st?.state === "ready" || st?.state === "running" || st?.weights_ready === true;
    case "missing":
      return !st?.extras_ready || (model.weights === "hub" && !st?.weights_ready);
    case "gpu":
      return model.requires_gpu;
    case "external":
      return model.engine === "external";
    default:
      return true;
  }
}

interface Props {
  models: ModelInfo[];
  gpu: GpuInfo | null;
  busy: string | null;
  envReady: boolean;
  onPrepare: (id: string, force: boolean) => void;
  onActivate: (id: string) => void;
  onDeactivate: (id: string) => void;
  onProbe: () => void;
}

export default function ModelsPanel({
  models,
  gpu,
  busy,
  envReady,
  onPrepare,
  onActivate,
  onDeactivate,
  onProbe,
}: Props) {
  const [filter, setFilter] = useState<Filter>("all");

  const counts = useMemo(() => {
    const out = {} as Record<Filter, number>;
    for (const f of FILTERS) out[f.id] = models.filter((m) => match(m, f.id)).length;
    return out;
  }, [models]);

  const sorted = useMemo(() => {
    const list = [...models];
    list.sort((a, b) => {
      const ar = a.status?.state === "ready" || a.status?.state === "running" ? 0 : 1;
      const br = b.status?.state === "ready" || b.status?.state === "running" ? 0 : 1;
      if (ar !== br) return ar - br;
      if (a.featured !== b.featured) return a.featured ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
    return list;
  }, [models]);

  const shown = sorted.filter((m) => match(m, filter));

  return (
    <div className="col" style={{ gap: 12 }}>
      <div className="row between">
        <h2 style={{ fontSize: 15 }}>模型</h2>
        <div className="row">
          <span className="muted small">共 {models.length} 个</span>
          <button className="btn small ghost" disabled={busy !== null} onClick={onProbe}>
            探测 Hub
          </button>
        </div>
      </div>

      <div className="chips">
        {FILTERS.map((f) => (
          <button
            key={f.id}
            className={`chip chip-btn ${filter === f.id ? "on" : ""}`}
            aria-pressed={filter === f.id}
            onClick={() => setFilter(f.id)}
          >
            {f.label} {counts[f.id]}
          </button>
        ))}
      </div>

      {!envReady && (
        <div className="banner">
          Python 依赖还没装好：点模型卡上的「下载权重」或「预装依赖」，会按模型自动装对应 pip extras。
        </div>
      )}
      {gpu && !gpu.available && (
        <div className="alert">
          没检测到可用的 CUDA/MPS：VLM 文档解析类模型（dots-ocr / deepseek-ocr 等）在这里只会显示「本机不可用」。
          CPU 上能跑的是 RapidOCR / Tesseract / PaddleOCR。
          {gpu.error ? `（${gpu.error}）` : ""}
        </div>
      )}

      {shown.length === 0 && <div className="empty">这个筛选条件下没有模型。</div>}
      {shown.map((model) => (
        <ModelCard
          key={model.id}
          model={model}
          busy={busy}
          onPrepare={onPrepare}
          onActivate={onActivate}
          onDeactivate={onDeactivate}
        />
      ))}
    </div>
  );
}
