import { useEffect, useMemo, useRef, useState } from "react";
import type { AppEvent } from "../types";

interface Props {
  events: AppEvent[];
}

const LEVELS = [
  { id: "all", label: "全部" },
  { id: "warn", label: "警告" },
  { id: "error", label: "错误" },
  { id: "progress", label: "进度" },
];

function clock(ts: number): string {
  const d = new Date(ts);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function describe(ev: AppEvent): { cls: string; source: string; text: string } {
  switch (ev.type) {
    case "log":
      return {
        cls: ev.level === "error" ? "error" : ev.level === "warn" ? "warn" : "",
        source: ev.source || "app",
        text: ev.message,
      };
    case "progress": {
      const pct = typeof ev.percent === "number" ? `${Math.round(ev.percent * 100)}% ` : "";
      const bytes = ev.bytes?.total
        ? ` (${(ev.bytes.done / 1048576).toFixed(1)}/${(ev.bytes.total / 1048576).toFixed(1)} MB)`
        : "";
      return {
        cls: "progress",
        source: ev.source || ev.job || "task",
        text: `${pct}${ev.stage ?? ""} ${ev.message ?? ""}${bytes}`.trim(),
      };
    }
    case "status":
      return { cls: "", source: "status", text: `${ev.model}: ${ev.state} ${ev.message ?? ""}`.trim() };
    case "env":
      return { cls: "progress", source: "env", text: `${ev.state} ${ev.message ?? ""}`.trim() };
    case "job":
      return {
        cls: ev.data.status === "error" ? "error" : ev.data.status === "done" ? "" : "progress",
        source: "job",
        text: `${ev.data.id} ${ev.data.status} ${ev.data.message ?? ""}`.trim(),
      };
    default:
      return { cls: "", source: "?", text: JSON.stringify(ev) };
  }
}

/** 事件流面板：SSE 实时日志 + 内存里保留最近 N 条。 */
export default function LogPanel({ events }: Props) {
  const [level, setLevel] = useState("all");
  const [autoScroll, setAutoScroll] = useState(true);
  const boxRef = useRef<HTMLDivElement | null>(null);

  const shown = useMemo(() => {
    const rows = events.map((ev) => ({ ev, info: describe(ev) }));
    if (level === "all") return rows;
    return rows.filter((r) => r.info.cls === level);
  }, [events, level]);

  useEffect(() => {
    if (!autoScroll) return;
    const box = boxRef.current;
    if (box) box.scrollTop = box.scrollHeight;
  }, [shown, autoScroll]);

  return (
    <div className="col" style={{ gap: 10 }}>
      <div className="row between">
        <h2 style={{ fontSize: 15 }}>日志</h2>
        <div className="row">
          {LEVELS.map((l) => (
            <button
              key={l.id}
              className={`chip chip-btn ${level === l.id ? "on" : ""}`}
              onClick={() => setLevel(l.id)}
            >
              {l.label}
            </button>
          ))}
          <label className="row tiny muted" style={{ gap: 5 }}>
            <input
              type="checkbox"
              checked={autoScroll}
              onChange={(e) => setAutoScroll(e.target.checked)}
            />
            自动滚动
          </label>
        </div>
      </div>

      <div className="logs" ref={boxRef}>
        {shown.length === 0 && <span className="muted">（还没有事件）</span>}
        {shown.map(({ ev, info }, i) => (
          <div className={`log-line ${info.cls}`} key={`${ev.ts}-${i}`}>
            <span className="ts">{clock(ev.ts)}</span>
            <span className="src">{info.source}</span>
            <span className="grow">{info.text}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
