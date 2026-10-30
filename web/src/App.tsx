import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, subscribeEvents } from "./api";
import EnvPanel from "./components/EnvPanel";
import HistoryPanel from "./components/HistoryPanel";
import LogPanel from "./components/LogPanel";
import ModelsPanel from "./components/ModelsPanel";
import RecognizePanel from "./components/RecognizePanel";
import SettingsPanel from "./components/SettingsPanel";
import type { AppEvent, AppInfo, EnvStatus, GpuInfo, Job, ModelInfo, Settings } from "./types";

const MAX_EVENTS = 400;

type Tab = "recognize" | "models" | "history" | "logs";

const NAV: { id: Tab; label: string; hint: string }[] = [
  { id: "recognize", label: "识别", hint: "图片 / PDF → 文本" },
  { id: "models", label: "模型", hint: "下载 · 启用 · 卸载" },
  { id: "history", label: "历史记录", hint: "产物与导出" },
  { id: "logs", label: "日志", hint: "进度与错误" },
];

export default function App() {
  const [tab, setTab] = useState<Tab>("recognize");
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [info, setInfo] = useState<AppInfo | null>(null);
  const [env, setEnv] = useState<EnvStatus | null>(null);
  const [envInfo, setEnvInfo] = useState<{ venv_python: string; base: string; detected: string }>({
    venv_python: "",
    base: "",
    detected: "",
  });
  const [gpu, setGpu] = useState<GpuInfo | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [events, setEvents] = useState<AppEvent[]>([]);
  const [job, setJob] = useState<Job | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [showEnv, setShowEnv] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [historyRevision, setHistoryRevision] = useState(0);

  const jobRef = useRef<Job | null>(null);
  jobRef.current = job;

  const onError = useCallback((message: string) => setError(message), []);

  const refreshModels = useCallback(async (probeHub = false) => {
    try {
      const list = await api.models(probeHub);
      setModels(list);
      setError(null);
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoaded(true);
    }
  }, [onError]);

  const refreshInfo = useCallback(async () => {
    try {
      const data = await api.info();
      setInfo(data);
      setEnv(data.env);
      setGpu(data.gpu);
      setSettings(data.settings);
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    }
  }, [onError]);

  const refreshEnv = useCallback(async () => {
    try {
      const data = await api.env();
      setEnv(data.env);
      setEnvInfo({
        venv_python: data.python ?? "",
        base: data.base ?? "",
        detected: data.detected ?? "",
      });
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    }
  }, [onError]);

  const refreshAll = useCallback(
    async (probeHub = false) => {
      await Promise.all([refreshModels(probeHub), refreshInfo()]);
    },
    [refreshModels, refreshInfo],
  );

  useEffect(() => {
    void refreshAll(false);
    api
      .logs(150)
      .then((past) => setEvents(past))
      .catch(() => {
        /* 日志拉不到不影响主流程 */
      });
  }, [refreshAll]);

  // 兜底轮询：后台任务 / Hub 预热完成后即使漏了 SSE 事件也能看到新状态
  useEffect(() => {
    const timer = window.setInterval(() => void refreshModels(false), 15000);
    return () => window.clearInterval(timer);
  }, [refreshModels]);

  // SSE：日志流 + 任务快照；status/env 变化时刷新模型列表
  useEffect(() => {
    let timer: number | null = null;
    const later = () => {
      if (timer !== null) return;
      timer = window.setTimeout(() => {
        timer = null;
        void refreshModels(false);
        void refreshEnv();
      }, 400);
    };

    const unsubscribe = subscribeEvents((ev) => {
      setEvents((prev) => {
        const next = [...prev, ev];
        return next.length > MAX_EVENTS ? next.slice(next.length - MAX_EVENTS) : next;
      });

      switch (ev.type) {
        case "status":
        case "env":
          later();
          break;
        case "job": {
          setJob(ev.data);
          if (ev.data.status !== "running") {
            setHistoryRevision((n) => n + 1);
            later();
          }
          break;
        }
        case "progress": {
          const current = jobRef.current;
          if (current && (!ev.job || ev.job === current.id)) {
            setJob({
              ...current,
              progress: ev.percent ?? current.progress,
              stage: ev.stage || current.stage,
              message: ev.message || current.message,
            });
          }
          break;
        }
        default:
          break;
      }
    });

    return () => {
      unsubscribe();
      if (timer !== null) window.clearTimeout(timer);
    };
  }, [refreshModels, refreshEnv]);

  const guard = useCallback(
    async (label: string, fn: () => Promise<unknown>, after?: () => void) => {
      setBusy(label);
      setError(null);
      try {
        await fn();
        after?.();
        await refreshModels(false);
        await refreshEnv();
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setBusy(null);
      }
    },
    [refreshModels, refreshEnv],
  );

  const onPrepare = useCallback(
    (id: string, force: boolean) =>
      guard(`准备 ${id}`, () => api.prepare(id, force), () => void refreshEnv()),
    [guard, refreshEnv],
  );
  const onActivate = useCallback((id: string) => guard(`启用 ${id}`, () => api.activate(id)), [guard]);
  const onDeactivate = useCallback(
    (id: string) => guard(`卸载 ${id}`, () => api.deactivate(id)),
    [guard],
  );

  const onSaveSettings = useCallback(
    (patch: Partial<Settings>) =>
      guard(
        "保存设置",
        async () => {
          setSettings(await api.saveSettings(patch));
        },
        () => {
          setShowSettings(false);
          void refreshInfo();
        },
      ),
    [guard, refreshInfo],
  );

  const onSaveLayout = useCallback(
    (patch: { models_dir?: string | null; out_dir?: string | null }) =>
      guard("更新目录", () => api.saveLayout(patch), () => void refreshInfo()),
    [guard, refreshInfo],
  );

  const onInstallEnv = useCallback(
    (extras: string[]) => guard(`安装依赖 ${extras.join(",") || "全部"}`, () => api.installEnv(extras)),
    [guard],
  );
  const onRecreateEnv = useCallback(
    () => guard("重建 venv", () => api.recreateEnv()),
    [guard],
  );
  const onSelectBase = useCallback(
    (base: string | null) => guard("切换基础解释器", () => api.selectBasePython(base)),
    [guard],
  );

  const activeModel = useMemo(() => models.find((m) => m.status?.state === "ready" || m.status?.state === "running"), [models]);
  const envReady = env?.state === "ready";

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          <img src="/logo.png" alt="OCR Manager" />
          <div>
            <strong>OCR Manager</strong>
            <span>多引擎文字识别</span>
          </div>
        </div>

        <nav className="nav">
          {NAV.map((item) => (
            <button
              key={item.id}
              className={`nav-item ${tab === item.id ? "on" : ""}`}
              onClick={() => setTab(item.id)}
            >
              {item.label}
              <small>{item.hint}</small>
            </button>
          ))}
        </nav>

        <div className="sidebar-foot">
          <button className="btn small ghost" onClick={() => setShowEnv(true)}>
            Python 环境
          </button>
          <button className="btn small ghost" onClick={() => setShowSettings(true)}>
            设置
          </button>
          <span className="tiny muted" style={{ padding: "4px 2px" }}>
            {activeModel ? `当前：${activeModel.name}` : "还没启用模型"}
          </span>
        </div>
      </aside>

      <main className="main">
        <header className="topbar">
          <div>
            <h1>{NAV.find((n) => n.id === tab)?.label}</h1>
            <div className="sub">
              {info ? `v${info.version} · 数据目录 ${info.root}` : "连接服务中…"}
            </div>
          </div>
          <div className="topbar-right">
            <span className={`pill ${envReady ? "pill-ok" : "pill-warn"}`} title={env?.message}>
              {env ? (envReady ? "python 就绪" : env.state) : "检测 python…"}
            </span>
            {gpu && (
              <span
                className={`pill ${gpu.available ? "pill-ok" : "pill-warn"}`}
                title={gpu.error || gpu.devices?.map((d) => d.name).join(", ")}
              >
                {gpu.available ? `GPU · ${gpu.devices?.[0]?.name ?? "可用"}` : "无 GPU"}
              </span>
            )}
            {!envReady && (
              <button className="btn small primary" disabled={busy !== null} onClick={() => setShowEnv(true)}>
                准备环境
              </button>
            )}
            <button
              className="btn small ghost"
              disabled={busy !== null}
              onClick={() => void refreshAll(true)}
            >
              刷新
            </button>
          </div>
        </header>

        {error && (
          <div className="alert" onClick={() => setError(null)} title="点击关闭">
            {error}
          </div>
        )}
        {busy && <div className="banner">{busy}…（详细过程见「日志」页）</div>}

        {tab === "recognize" && (
          <RecognizePanel
            models={models}
            job={job}
            onJobStarted={(created) => setJob(created)}
            onFinished={() => setHistoryRevision((n) => n + 1)}
            onError={onError}
          />
        )}

        {tab === "models" && (
          <>
            {!loaded && <div className="empty">加载模型中…</div>}
            {loaded && (
              <ModelsPanel
                models={models}
                gpu={gpu}
                busy={busy}
                envReady={envReady}
                onPrepare={onPrepare}
                onActivate={onActivate}
                onDeactivate={onDeactivate}
                onProbe={() => void refreshAll(true)}
              />
            )}
          </>
        )}

        {tab === "history" && <HistoryPanel revision={historyRevision} onError={onError} />}

        {tab === "logs" && <LogPanel events={events} />}

        <footer className="footer">
          <span>API: /api/models · /api/recognize · /api/history · /api/events</span>
          {info?.dirs?.models && <span>权重: {info.dirs.models}</span>}
          {info?.dirs?.output && <span>产物: {info.dirs.output}</span>}
          {info?.tesseract && <span>tesseract: {info.tesseract}</span>}
        </footer>
      </main>

      <EnvPanel
        open={showEnv}
        onClose={() => setShowEnv(false)}
        env={env}
        python={envInfo.detected}
        venv={envInfo.venv_python}
        busy={busy}
        onInstall={onInstallEnv}
        onRecreate={onRecreateEnv}
        onSelectBase={onSelectBase}
        onDetect={() => void refreshEnv()}
      />

      <SettingsPanel
        open={showSettings}
        onClose={() => setShowSettings(false)}
        settings={settings}
        dirs={info?.dirs ?? {}}
        busy={busy}
        onSaveSettings={onSaveSettings}
        onSaveLayout={onSaveLayout}
        onReloadCatalog={() => guard("重读清单", () => refreshAll(true))}
      />
    </div>
  );
}
