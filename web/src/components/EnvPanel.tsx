import { useState } from "react";
import type { EnvStatus } from "../types";
import Modal from "./Modal";

interface Props {
  open: boolean;
  onClose: () => void;
  env: EnvStatus | null;
  python: string;
  venv: string;
  busy: string | null;
  onInstall: (extras: string[]) => void;
  onRecreate: () => void;
  onSelectBase: (base: string | null) => void;
  onDetect: () => void;
}

const ALL_EXTRAS = ["rapidocr", "tesseract", "paddle", "vlm"];

/** Python 环境弹层：venv 状态、extras 安装、重建、指定基础解释器。 */
export default function EnvPanel({
  open,
  onClose,
  env,
  python,
  venv,
  busy,
  onInstall,
  onRecreate,
  onSelectBase,
  onDetect,
}: Props) {
  const [base, setBase] = useState("");

  return (
    <Modal
      open={open}
      title="Python 环境"
      subtitle="依赖装在独立的 venv 里，不会污染系统 Python"
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn ghost" onClick={onDetect} disabled={busy !== null}>
            重新探测
          </button>
          <button className="btn" onClick={() => onInstall(ALL_EXTRAS)} disabled={busy !== null}>
            安装全部引擎依赖
          </button>
          <button className="btn danger" onClick={onRecreate} disabled={busy !== null}>
            重建 venv
          </button>
        </>
      }
    >
      <div className="row">
        <span className={`pill ${env?.state === "ready" ? "pill-ok" : env?.state === "error" ? "pill-bad" : "pill-warn"}`}>
          {env?.state ?? "unknown"}
        </span>
        <span className="small muted">{env?.message ?? "还没探测"}</span>
      </div>

      <div className="kv">
        <span>基础解释器</span>
        <span className="mono tiny">{env?.base || "（自动探测）"}</span>
        <span>venv 解释器</span>
        <span className="mono tiny">{venv || "（未创建）"}</span>
        <span>系统 python</span>
        <span className="mono tiny">{python || "（没找到）"}</span>
      </div>

      <div className="col" style={{ gap: 6 }}>
        <span className="muted small">引擎依赖（pip extras）</span>
        {ALL_EXTRAS.map((name) => {
          const ok = env?.extras?.[name] === true;
          return (
            <div className="row between" key={name}>
              <span className="row" style={{ gap: 8 }}>
                <span className={`pill ${ok ? "pill-ok" : ""}`}>{ok ? "已安装" : "未安装"}</span>
                <span className="small mono">{name}</span>
              </span>
              <button className="btn small ghost" disabled={busy !== null} onClick={() => onInstall([name])}>
                {ok ? "重新安装" : "安装"}
              </button>
            </div>
          );
        })}
      </div>

      {env?.probe_errors && env.probe_errors.length > 0 && (
        <div className="col" style={{ gap: 4 }}>
          <span className="muted small">探测报错</span>
          {env.probe_errors.map((line, i) => (
            <code key={i} className="mono tiny" style={{ wordBreak: "break-all" }}>
              {line}
            </code>
          ))}
        </div>
      )}

      <label className="field">
        指定基础解释器（留空 = 自动探测）
        <div className="row">
          <input
            className="grow"
            type="text"
            placeholder="/usr/bin/python3 或 C:\\Python312\\python.exe"
            value={base}
            onChange={(e) => setBase(e.target.value)}
          />
          <button className="btn small" disabled={busy !== null} onClick={() => onSelectBase(base.trim() || null)}>
            保存并重建
          </button>
        </div>
      </label>

      <p className="tiny muted" style={{ margin: 0 }}>
        点上面的「安装依赖」装全部引擎依赖；只想要某个引擎时，去「模型」页点那张卡片的「下载」。
        基础解释器变了以后 venv 会自动重建。
      </p>
    </Modal>
  );
}
