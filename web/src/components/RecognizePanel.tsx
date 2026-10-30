import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import type { Job, ModelInfo, OcrResult } from "../types";
import ResultViewer from "./ResultViewer";

interface Props {
  models: ModelInfo[];
  /** 当前任务（由 App 通过 SSE job 事件维护） */
  job: Job | null;
  onJobStarted: (job: Job) => void;
  onFinished: () => void;
  onError: (message: string) => void;
}

interface PickedFile {
  file: File;
  /** 图片预览用的 blob URL */
  preview: string | null;
}

const ACCEPT = "image/*,.pdf";

function isImage(name: string): boolean {
  return /\.(png|jpe?g|gif|bmp|webp|tiff?)$/i.test(name);
}

function human(bytes: number): string {
  if (bytes > 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  if (bytes > 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
}

export default function RecognizePanel({ models, job, onJobStarted, onFinished, onError }: Props) {
  const [picked, setPicked] = useState<PickedFile[]>([]);
  const [over, setOver] = useState(false);
  const [modelId, setModelId] = useState("");
  const [maxPages, setMaxPages] = useState(20);
  const [pdfDpi, setPdfDpi] = useState(200);
  const [result, setResult] = useState<OcrResult | null>(null);
  const [starting, setStarting] = useState(false);
  // 预览面板：可以关掉（列表就占满宽度），宽度靠中间的分隔条拖动
  const [previewOpen, setPreviewOpen] = useState(true);
  const [listPct, setListPct] = useState(25);
  const [selected, setSelected] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const splitRef = useRef<HTMLDivElement | null>(null);

  // 拖动分隔条：左列表占容器宽度的百分比，夹在 15%–60% 之间
  const startDrag = useCallback((event: React.MouseEvent) => {
    event.preventDefault();
    const box = splitRef.current;
    if (!box) return;
    const move = (e: MouseEvent) => {
      const rect = box.getBoundingClientRect();
      if (rect.width <= 0) return;
      const pct = ((e.clientX - rect.left) / rect.width) * 100;
      setListPct(Math.min(60, Math.max(15, pct)));
    };
    const up = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  }, []);

  const usable = useMemo(
    () => models.filter((m) => m.status?.state !== "unavailable"),
    [models],
  );

  // 默认选「已就绪」里排序最靠前的那个，其次推荐模型
  useEffect(() => {
    if (modelId && usable.some((m) => m.id === modelId)) return;
    const ready = usable.find((m) => m.status?.state === "ready" || m.status?.state === "running");
    const featured = usable.find((m) => m.featured);
    setModelId((ready ?? featured ?? usable[0])?.id ?? "");
  }, [usable, modelId]);

  // 任务完成时把历史里的结果拉回来（同一个 job 只拉一次）
  const doneRef = useRef<string | null>(null);
  useEffect(() => {
    if (!job || job.status !== "done" || !job.id) return;
    if (doneRef.current === job.id) return;
    doneRef.current = job.id;
    let alive = true;
    api
      .historyItem(job.id)
      .then((body) => {
        if (alive) setResult(body.result);
      })
      .catch(() => {
        /* 结果拉不到就让用户去历史页看，别盖住任务状态 */
      });
    onFinished();
    return () => {
      alive = false;
    };
  }, [job, onFinished]);

  const addFiles = useCallback((files: FileList | File[]) => {
    const list = Array.from(files);
    setPicked((prev) => {
      const next = [...prev];
      for (const file of list) {
        if (next.some((p) => p.file.name === file.name && p.file.size === file.size)) continue;
        next.push({ file, preview: isImage(file.name) ? URL.createObjectURL(file) : null });
      }
      return next;
    });
  }, []);

  const status = job?.status ?? "idle";
  const running = status === "running";

  const start = useCallback(async () => {
    if (picked.length === 0) {
      onError("先选一张图片或 PDF");
      return;
    }
    if (!modelId) {
      onError("先选一个 OCR 模型");
      return;
    }
    setStarting(true);
    setResult(null);
    try {
      const created = await api.recognize(
        picked.map((p) => p.file),
        modelId,
        { max_pages: maxPages, pdf_dpi: pdfDpi },
      );
      onJobStarted(created);
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    } finally {
      setStarting(false);
    }
  }, [picked, modelId, maxPages, pdfDpi, onJobStarted, onError]);

  const cancel = useCallback(async () => {
    if (!job) return;
    try {
      await api.cancelJob(job.id);
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    }
  }, [job, onError]);

  const exportFile = useCallback(
    (name: string) => {
      if (!job) return;
      window.open(api.fileUrl(job.id, name, true), "_blank");
    },
    [job],
  );

  return (
    <div className="recognize">
      <div className="col" style={{ gap: 12 }}>
        <div className="card">
          <div className="row between">
            <h2 style={{ fontSize: 15 }}>输入</h2>
            <span className="muted tiny">图片 / PDF · 支持多选</span>
          </div>

          <input
            ref={inputRef}
            type="file"
            accept={ACCEPT}
            multiple
            hidden
            onChange={(e) => {
              if (e.target.files) addFiles(e.target.files);
              e.target.value = "";
            }}
          />

          <div className="row between" style={{ marginBottom: 8 }}>
            <button className="btn small primary" onClick={() => inputRef.current?.click()} disabled={running}>
              选择文件
            </button>
            <span className="muted tiny">
              {picked.length === 0 ? "图片与 PDF 可以混选，一次识别全部" : `${picked.length} 个文件`}
            </span>
            <span className="grow" />
            {picked.length > 0 && (
              <button className="btn small ghost" onClick={() => setPreviewOpen((v) => !v)}>
                {previewOpen ? "收起预览" : "展开预览"}
              </button>
            )}
            {picked.length > 0 && !running && (
              <button
                className="btn small ghost"
                onClick={() => {
                  picked.forEach((p) => p.preview && URL.revokeObjectURL(p.preview));
                  setPicked([]);
                  setSelected(0);
                }}
              >
                清空
              </button>
            )}
          </div>

          <div className="split" ref={splitRef}>
            <div
              className={`split-list ${over ? "over" : ""}`}
              style={{ width: previewOpen ? `${listPct}%` : "100%" }}
              onDragOver={(e) => {
                e.preventDefault();
                setOver(true);
              }}
              onDragLeave={() => setOver(false)}
              onDrop={(e) => {
                e.preventDefault();
                setOver(false);
                addFiles(e.dataTransfer.files);
              }}
            >
              {picked.length === 0 ? (
                <div
                  className="split-empty"
                  onClick={() => inputRef.current?.click()}
                  role="button"
                  tabIndex={0}
                >
                  把图片或 PDF 拖进来，或点「选择文件」
                  <div className="tiny muted" style={{ marginTop: 6 }}>
                    图片 png / jpg / webp / bmp ｜ PDF 逐页渲染后识别
                  </div>
                </div>
              ) : (
                picked.map((p, i) => (
                  <button
                    key={`${p.file.name}-${i}`}
                    className={`file-item ${i === selected ? "on" : ""}`}
                    onClick={() => setSelected(i)}
                  >
                    {p.preview ? (
                      <img src={p.preview} alt="" />
                    ) : (
                      <span className="chip tiny">PDF</span>
                    )}
                    <span className="grow" style={{ wordBreak: "break-all", textAlign: "left" }}>
                      {p.file.name}
                    </span>
                    <span className="muted tiny">{human(p.file.size)}</span>
                    <span
                      className="file-remove"
                      role="button"
                      tabIndex={0}
                      aria-label="移除"
                      onClick={(e) => {
                        e.stopPropagation();
                        if (p.preview) URL.revokeObjectURL(p.preview);
                        setPicked((prev) => prev.filter((_, idx) => idx !== i));
                        setSelected((cur) => (cur >= i ? Math.max(0, cur - 1) : cur));
                      }}
                    >
                      ✕
                    </span>
                  </button>
                ))
              )}
            </div>

            {previewOpen && picked.length > 0 && (
              <>
                <div
                  className="split-handle"
                  onMouseDown={startDrag}
                  title="拖动调整列表与预览的宽度"
                  role="separator"
                  aria-orientation="vertical"
                />
                <div className="split-preview">
                  <div className="row between">
                    <span className="small mono" style={{ wordBreak: "break-all" }}>
                      {picked[selected]?.file.name ?? ""}
                    </span>
                    <button
                      className="btn small ghost"
                      aria-label="关闭预览"
                      onClick={() => setPreviewOpen(false)}
                    >
                      ✕
                    </button>
                  </div>
                  {picked[selected]?.preview ? (
                    <img className="preview-img" src={picked[selected].preview ?? ""} alt="" />
                  ) : (
                    <div className="split-empty">
                      PDF 要等识别后才能在结果里看每一页；这里只显示图片预览。
                    </div>
                  )}
                </div>
              </>
            )}
          </div>

          <label className="field">
            识别模型
            <select value={modelId} onChange={(e) => setModelId(e.target.value)} disabled={running}>
              {usable.length === 0 && <option value="">没有可用模型（先在「模型」页启用）</option>}
              {usable.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                  {m.status?.state === "ready" ? "（已就绪）" : ""}
                  {m.status?.weights_ready || m.weights === "none" ? "" : "（需下载权重）"}
                </option>
              ))}
            </select>
          </label>

          <div className="row" style={{ gap: 10 }}>
            <label className="field grow">
              PDF 最多页数
              <input
                type="number"
                min={1}
                max={500}
                value={maxPages}
                disabled={running}
                onChange={(e) => setMaxPages(Math.max(1, Number(e.target.value) || 1))}
              />
            </label>
            <label className="field grow">
              PDF 渲染 DPI
              <input
                type="number"
                min={72}
                max={400}
                step={10}
                value={pdfDpi}
                disabled={running}
                onChange={(e) => setPdfDpi(Math.max(72, Number(e.target.value) || 200))}
              />
            </label>
          </div>

          <div className="row">
            <button className="btn primary" disabled={starting || running || picked.length === 0} onClick={start}>
              {starting ? "提交中…" : "开始识别"}
            </button>
            {running && (
              <button className="btn danger" onClick={cancel}>
                取消
              </button>
            )}
          </div>
        </div>

        {job && (
          <div className="card">
            <div className="row between">
              <span className="card-title">
                {job.status === "running" ? "识别中" : job.status === "done" ? "已完成" : job.status === "canceled" ? "已取消" : "失败"}
              </span>
              <span className="muted tiny mono">{job.id}</span>
            </div>
            <div className="progress">
              <span style={{ width: `${Math.round((job.progress || 0) * 100)}%` }} />
            </div>
            <div className="small muted">
              {job.message || job.stage || "等待中…"}
              {job.status === "done" &&
                ` · ${job.pages} 页 / ${job.blocks} 块 / ${job.chars} 字 · ${(job.duration_ms / 1000).toFixed(2)}s`}
            </div>
            {job.error && <div className="small" style={{ color: "var(--danger)" }}>{job.error}</div>}
          </div>
        )}
      </div>

      <div className="card">
        {result ? (
          <ResultViewer result={result} jobId={job?.id} onExport={exportFile} />
        ) : (
          <div className="empty">
            {running ? "识别进行中，进度见左侧。" : "识别完成后这里显示 text / markdown / 分块结果。"}
          </div>
        )}
      </div>
    </div>
  );
}
