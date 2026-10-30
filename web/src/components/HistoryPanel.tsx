import { useCallback, useEffect, useState } from "react";
import { api } from "../api";
import type { HistoryEntry, OcrResult } from "../types";
import ResultViewer from "./ResultViewer";

interface Props {
  /** 每次识别成功 +1，触发重新拉取 */
  revision: number;
  onError: (message: string) => void;
}

function when(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export default function HistoryPanel({ revision, onError }: Props) {
  const [items, setItems] = useState<HistoryEntry[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<{ entry: HistoryEntry; result: OcrResult } | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      setItems(await api.history(200));
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [onError]);

  useEffect(() => {
    void load();
  }, [load, revision]);

  useEffect(() => {
    if (!selected) {
      setDetail(null);
      return;
    }
    let alive = true;
    api
      .historyItem(selected)
      .then((body) => {
        if (alive) setDetail(body);
      })
      .catch((err) => onError(err instanceof Error ? err.message : String(err)));
    return () => {
      alive = false;
    };
  }, [selected, onError]);

  const remove = useCallback(
    async (id: string) => {
      try {
        await api.deleteHistory(id);
        if (selected === id) setSelected(null);
        await load();
      } catch (err) {
        onError(err instanceof Error ? err.message : String(err));
      }
    },
    [selected, load, onError],
  );

  return (
    <div className="col" style={{ gap: 12 }}>
      <div className="row between">
        <h2 style={{ fontSize: 15 }}>历史记录</h2>
        <div className="row">
          <span className="muted small">{items.length} 条</span>
          <button className="btn small ghost" onClick={() => void load()}>
            刷新
          </button>
        </div>
      </div>

      {loading && <div className="empty">加载中…</div>}
      {!loading && items.length === 0 && <div className="empty">还没有识别记录，去「识别」页跑一张试试。</div>}

      <div className="grid-2" style={{ gridTemplateColumns: "minmax(0, 380px) minmax(0, 1fr)" }}>
        <div className="list">
          {items.map((item) => (
            <div
              key={item.id}
              className={`list-item ${selected === item.id ? "on" : ""}`}
              onClick={() => setSelected(item.id)}
            >
              <div className="grow col" style={{ gap: 2 }}>
                <span className="small">
                  {item.model_name || item.model}
                  <span className={`pill ${item.status === "done" ? "pill-ok" : "pill-bad"}`} style={{ marginLeft: 6 }}>
                    {item.status}
                  </span>
                </span>
                <span className="tiny muted">
                  {when(item.created_at)} · {item.source_name} · {item.pages} 页 / {item.chars} 字
                </span>
                {item.preview && <span className="tiny muted" style={{ opacity: 0.75 }}>{item.preview.slice(0, 60)}</span>}
              </div>
              <button
                className="btn small ghost"
                onClick={(e) => {
                  e.stopPropagation();
                  void remove(item.id);
                }}
                aria-label="删除"
              >
                ✕
              </button>
            </div>
          ))}
        </div>

        <div className="card" style={{ minHeight: 200 }}>
          {!detail && <div className="empty">选左边一条记录查看结果。</div>}
          {detail && (
            <>
              <div className="kv">
                <span>来源文件</span>
                <span>{detail.entry.source_name}</span>
                <span>模型</span>
                <span>
                  {detail.entry.model_name}（{detail.entry.engine}）
                </span>
                <span>尺寸 / 页数</span>
                <span>
                  {detail.entry.width}×{detail.entry.height} · {detail.entry.pages} 页
                </span>
                <span>耗时</span>
                <span>{(detail.entry.duration_ms / 1000).toFixed(2)} s</span>
                <span>产物目录</span>
                <span className="mono tiny">{detail.entry.id}</span>
              </div>
              <div className="row">
                <a
                  className="btn small ghost"
                  href={api.fileUrl(detail.entry.id, detail.entry.input_file, true)}
                  target="_blank"
                  rel="noreferrer"
                >
                  输入原件
                </a>
                {["result.md", "result.txt", "result.json"].map((name) => (
                  <a
                    key={name}
                    className="btn small ghost"
                    href={api.fileUrl(detail.entry.id, name, true)}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {name}
                  </a>
                ))}
              </div>
              <ResultViewer result={detail.result} jobId={detail.entry.id} onExport={(name) => window.open(api.fileUrl(detail.entry.id, name, true), "_blank")} />
            </>
          )}
        </div>
      </div>
    </div>
  );
}
