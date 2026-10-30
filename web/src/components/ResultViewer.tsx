import { useState } from "react";
import type { OcrResult, Page } from "../types";

type Tab = "text" | "markdown" | "blocks";

interface Props {
  result: OcrResult;
  /** 有 job id 时给导出按钮用 */
  jobId?: string | null;
  onExport?: (name: string) => void;
}

function pageText(page: Page, tab: Tab): string {
  if (tab === "markdown") return page.markdown || page.text;
  return page.text || page.markdown;
}

/** 识别结果查看：纯文本 / Markdown / 分块（含置信度）。 */
export default function ResultViewer({ result, jobId, onExport }: Props) {
  const [pageIndex, setPageIndex] = useState(0);
  const [tab, setTab] = useState<Tab>("text");
  const pages = result.pages ?? [];
  const page = pages[Math.min(pageIndex, Math.max(pages.length - 1, 0))];

  return (
    <div className="col" style={{ gap: 10 }}>
      <div className="row between">
        <div className="row">
          <span className="pill pill-ok">完成</span>
          <span className="muted small">
            {pages.length} 页 · 引擎 <code>{result.engine}</code>
            {result.duration_ms > 0 && ` · ${(result.duration_ms / 1000).toFixed(2)}s`}
          </span>
        </div>
        <div className="row">
          {jobId && onExport && (
            <>
              <button className="btn small ghost" onClick={() => onExport("result.md")}>
                导出 .md
              </button>
              <button className="btn small ghost" onClick={() => onExport("result.txt")}>
                导出 .txt
              </button>
              <button className="btn small ghost" onClick={() => onExport("result.json")}>
                导出 .json
              </button>
            </>
          )}
        </div>
      </div>

      {pages.length > 1 && (
        <div className="pages">
          {pages.map((_p, i) => (
            <button
              key={i}
              className={`chip chip-btn ${i === pageIndex ? "on" : ""}`}
              onClick={() => setPageIndex(i)}
            >
              第 {i + 1} 页
            </button>
          ))}
        </div>
      )}

      <div className="tabs">
        {(["text", "markdown", "blocks"] as Tab[]).map((t) => (
          <button key={t} className={`tab ${tab === t ? "on" : ""}`} onClick={() => setTab(t)}>
            {t === "text" ? "纯文本" : t === "markdown" ? "Markdown" : `分块 ${page?.blocks?.length ?? 0}`}
          </button>
        ))}
        <span className="grow" />
        {page && (
          <span className="tiny muted">
            {page.width}×{page.height}
          </span>
        )}
      </div>

      {!page && <div className="empty">这次识别没有返回任何页面。</div>}

      {page && tab !== "blocks" && <pre className="text">{pageText(page, tab) || "（空）"}</pre>}

      {page && tab === "blocks" && (
        <div className="text" style={{ maxHeight: 460, overflow: "auto", padding: "8px 12px" }}>
          {page.blocks.length === 0 && <span className="muted">没有分块信息。</span>}
          {page.blocks.map((b, i) => (
            <div className="block-row" key={i}>
              <span className="chip tiny">{b.kind || "text"}</span>
              <span className="grow" style={{ wordBreak: "break-word" }}>
                {b.text}
              </span>
              <span className="conf">
                {typeof b.confidence === "number" ? b.confidence.toFixed(3) : "—"}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
