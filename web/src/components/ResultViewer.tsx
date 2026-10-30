import { useMemo, useState } from "react";
import { api } from "../api";
import type { OcrResult, Page } from "../types";

type Tab = "text" | "markdown" | "blocks";

interface Props {
  result: OcrResult;
  /** 有 job id 时给导出按钮用 */
  jobId?: string | null;
  onExport?: (name: string) => void;
}

/** 把产物目录里的副本名（02-invoice.pdf）还原成用户看得懂的名字 */
function displaySource(name?: string): string {
  if (!name) return "";
  const base = name.split(/[\\/]/).pop() ?? name;
  return /^\d\d-/.test(base) ? base.slice(3) : base;
}

/** 一个输入文件对应的一组页 */
interface SourceGroup {
  key: string;
  label: string;
  pages: Page[];
}

function groupBySource(pages: Page[]): SourceGroup[] {
  const groups: SourceGroup[] = [];
  const index = new Map<string, number>();
  for (const page of pages) {
    const key = page.source ?? "";
    let at = index.get(key);
    if (at === undefined) {
      at = groups.length;
      index.set(key, at);
      groups.push({ key, label: displaySource(key) || "全部页", pages: [] });
    }
    groups[at].pages.push(page);
  }
  return groups;
}

function pageText(page: Page, tab: Tab): string {
  if (tab === "markdown") return page.markdown || page.text;
  return page.text || page.markdown;
}

/** 识别结果查看：纯文本 / Markdown / 分块（含置信度）。 */
export default function ResultViewer({ result, jobId, onExport }: Props) {
  const [pageIndex, setPageIndex] = useState(0);
  const [tab, setTab] = useState<Tab>("text");
  const [sourceKey, setSourceKey] = useState<string | null>(null);
  const groups = useMemo(() => groupBySource(result.pages ?? []), [result.pages]);
  const active = Math.max(
    groups.findIndex((g) => g.key === sourceKey),
    0,
  );
  const pages = groups[active]?.pages ?? [];
  const page = pages[Math.min(pageIndex, Math.max(pages.length - 1, 0))];

  return (
    <div className="col" style={{ gap: 10 }}>
      <div className="row between">
        <div className="row">
          <span className="pill pill-ok">完成</span>
          <span className="muted small">
            {groups.length > 1 && `${groups.length} 个文件 · `}
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
              <a className="btn small primary" href={api.xlsxUrl(jobId)}>
                导出 Excel
              </a>
            </>
          )}
        </div>
      </div>

      {groups.length > 1 && (
        <div className="tabs">
          {groups.map((group) => (
            <button
              key={group.key || "all"}
              className={`tab ${group.key === groups[active]?.key ? "on" : ""}`}
              onClick={() => {
                setSourceKey(group.key);
                setPageIndex(0);
              }}
            >
              {group.label} ({group.pages.length})
            </button>
          ))}
        </div>
      )}

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
