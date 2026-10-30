import { useEffect, useState } from "react";
import type { Settings } from "../types";
import Modal from "./Modal";

interface Props {
  open: boolean;
  onClose: () => void;
  settings: Settings | null;
  dirs: Record<string, string>;
  busy: string | null;
  onSaveSettings: (patch: Partial<Settings>) => void;
  onSaveLayout: (patch: { models_dir?: string | null; out_dir?: string | null }) => void;
  onReloadCatalog: () => void;
}

const DEFAULT_LAYOUT = { models_dir: "", out_dir: "" };

/** 设置弹层：识别默认参数、HF 镜像、目录覆盖。 */
export default function SettingsPanel({
  open,
  onClose,
  settings,
  dirs,
  busy,
  onSaveSettings,
  onSaveLayout,
  onReloadCatalog,
}: Props) {
  const [form, setForm] = useState<Settings | null>(settings);
  const [layout, setLayout] = useState(DEFAULT_LAYOUT);

  // 每次打开都用最新值重置
  useEffect(() => {
    if (open) {
      setForm(settings);
      setLayout(DEFAULT_LAYOUT);
    }
  }, [open, settings]);

  const patch = (next: Partial<Settings>) => setForm((prev) => (prev ? { ...prev, ...next } : prev));

  return (
    <Modal
      open={open}
      title="设置"
      subtitle="改了马上生效，落盘在 $OCR_HOME/config/settings.json"
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn ghost" onClick={onClose}>
            关闭
          </button>
          <button
            className="btn primary"
            disabled={busy !== null || !form}
            onClick={() => {
              if (form) onSaveSettings(form);
              if (layout.models_dir || layout.out_dir) onSaveLayout(layout);
            }}
          >
            保存
          </button>
        </>
      }
    >
      {!form && <div className="empty">还没读到设置。</div>}
      {form && (
        <>
          <div className="grid-2">
            <label className="field">
              Hub 镜像 / 端点
              <input
                type="text"
                placeholder="https://hf-mirror.com（留空用官方）"
                value={form.hf_endpoint}
                onChange={(e) => patch({ hf_endpoint: e.target.value })}
              />
            </label>
            <label className="field">
              默认识别模型
              <input
                type="text"
                placeholder="例如 rapidocr"
                value={form.default_model}
                onChange={(e) => patch({ default_model: e.target.value })}
              />
            </label>
          </div>

          <div className="grid-2">
            <label className="field">
              PDF 最多页数
              <input
                type="number"
                min={1}
                value={form.max_pages}
                onChange={(e) => patch({ max_pages: Math.max(1, Number(e.target.value) || 1) })}
              />
            </label>
            <label className="field">
              PDF 渲染 DPI
              <input
                type="number"
                min={72}
                step={10}
                value={form.pdf_dpi}
                onChange={(e) => patch({ pdf_dpi: Math.max(72, Number(e.target.value) || 200) })}
              />
            </label>
            <label className="field">
              图片最长边（0 = 不缩放）
              <input
                type="number"
                min={0}
                step={100}
                value={form.max_side}
                onChange={(e) => patch({ max_side: Math.max(0, Number(e.target.value) || 0) })}
              />
            </label>
            <label className="field">
              启动时自动加载默认模型
              <select
                value={form.auto_load ? "on" : "off"}
                onChange={(e) => patch({ auto_load: e.target.value === "on" })}
              >
                <option value="on">开启（加载慢一点，但第一张图更快）</option>
                <option value="off">关闭（用到时再加载）</option>
              </select>
            </label>
          </div>

          <div className="col" style={{ gap: 6 }}>
            <span className="muted small">目录（留空 = 用默认值）</span>
            <div className="kv">
              <span>数据根目录</span>
              <span className="mono tiny">{dirs.root || "—"}</span>
              <span>权重目录</span>
              <span className="mono tiny">{dirs.models || "—"}</span>
              <span>产物目录</span>
              <span className="mono tiny">{dirs.output || "—"}</span>
              <span>venv</span>
              <span className="mono tiny">{dirs.venv || "—"}</span>
            </div>
          </div>

          <div className="grid-2">
            <label className="field">
              权重目录覆盖
              <input
                type="text"
                placeholder={dirs.models || "默认"}
                value={layout.models_dir}
                onChange={(e) => setLayout((prev) => ({ ...prev, models_dir: e.target.value }))}
              />
            </label>
            <label className="field">
              产物目录覆盖
              <input
                type="text"
                placeholder={dirs.output || "默认"}
                value={layout.out_dir}
                onChange={(e) => setLayout((prev) => ({ ...prev, out_dir: e.target.value }))}
              />
            </label>
          </div>

          <div className="row">
            <button className="btn small ghost" disabled={busy !== null} onClick={onReloadCatalog}>
              重新读取 models.toml
            </button>
            <span className="tiny muted">
              想加自己的模型：编辑 <code>$OCR_HOME/config/models.toml</code>，再点这里。
            </span>
          </div>
        </>
      )}
    </Modal>
  );
}
