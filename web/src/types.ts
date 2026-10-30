/** 与 Go 侧 JSON 一一对应的类型（internal/models、internal/core、internal/history）。 */

export interface RepoFile {
  name: string;
  bytes: number;
}

export interface RepoInfo {
  repo: string;
  exists: boolean;
  reachable: boolean;
  endpoint: string;
  pipeline_tag?: string | null;
  library_name?: string | null;
  tags: string[];
  files: RepoFile[];
  total_bytes: number;
  downloads: number;
  likes: number;
  last_modified?: string | null;
  gated: boolean;
  private: boolean;
  error?: string | null;
}

export interface WeightsInfo {
  ready: boolean;
  path: string;
  bytes: number;
  files: number;
  at: number;
}

/** core.ModelStatus */
export interface ModelStatus {
  id: string;
  state: "idle" | "preparing" | "loading" | "ready" | "running" | "error" | "unavailable" | string;
  message: string;
  error?: string;
  loaded: boolean;
  weights_ready: boolean;
  needs_download: boolean;
  extras: string[];
  extras_ready: boolean;
  requires_gpu: boolean;
  gpu_ok: boolean;
  workers: number;
  weights: WeightsInfo;
  repo?: RepoInfo | null;
  updated_at: number;
}

/** core.ModelView = 清单条目 + 状态 */
export interface ModelInfo {
  id: string;
  name: string;
  engine: string;
  repo: string;
  homepage: string;
  description: string;
  languages: string[];
  tasks: string[];
  weights: "hub" | "auto" | "none" | string;
  extras: string[];
  requires_gpu: boolean;
  platforms: string[];
  hidden: boolean;
  size_hint: string;
  tags: string[];
  featured: boolean;
  probe: boolean;
  options: Record<string, unknown>;
  status: ModelStatus;
}

export interface EnvStatus {
  state: "missing" | "creating" | "installing" | "waiting" | "ready" | "error" | string;
  message: string;
  base?: string | null;
  venv_python: string;
  venv_ready: boolean;
  extras: Record<string, boolean>;
  probe_errors: string[] | null;
  updated_at: number;
}

export interface GpuDevice {
  index: number;
  name: string;
  memory_mb: number;
  driver: string;
}

export interface GpuInfo {
  available: boolean;
  devices: GpuDevice[] | null;
  error?: string;
  checked_at: number;
}

export interface Block {
  text: string;
  kind: string;
  box: number[][] | null;
  confidence?: number | null;
}

export interface Page {
  index: number;
  /** 这一页来自哪个输入文件（多文件一次识别时用来分 tab） */
  source?: string;
  width: number;
  height: number;
  text: string;
  markdown: string;
  blocks: Block[];
}

export interface OcrResult {
  engine: string;
  pages: Page[];
  meta?: Record<string, unknown> | null;
  duration_ms: number;
}

export interface HistoryEntry {
  id: string;
  model: string;
  model_name: string;
  engine: string;
  status: string;
  error?: string;
  source_name: string;
  source_kind: string;
  input_file: string;
  bytes: number;
  created_at: number;
  duration_ms: number;
  pages: number;
  blocks: number;
  chars: number;
  width: number;
  height: number;
  preview: string;
  options?: Record<string, unknown> | null;
}

export interface Job {
  id: string;
  model: string;
  model_name: string;
  status: "running" | "done" | "error" | "canceled" | string;
  stage: string;
  message: string;
  progress: number;
  source_name: string;
  source_kind: string;
  pages: number;
  blocks: number;
  chars: number;
  duration_ms: number;
  error?: string;
  entry?: HistoryEntry | null;
  created_at: number;
  finished_at: number;
}

export interface Settings {
  default_model: string;
  active_model: string;
  hf_endpoint: string;
  auto_load: boolean;
  max_pages: number;
  pdf_dpi: number;
  max_side: number;
  options: Record<string, Record<string, unknown>>;
}

export interface AppInfo {
  version: string;
  root: string;
  dirs: Record<string, string>;
  active: string;
  settings: Settings;
  env: EnvStatus;
  op?: Op | null;
  gpu: GpuInfo;
  models: number;
  visible: number;
  sources: string[];
  tesseract: string;
  docs: Record<string, string>;
}

export interface ProgressBytes {
  done: number;
  total: number;
  files_done: number;
  files_total: number;
  file?: string | null;
  file_done: number;
  file_total: number;
}

export interface LogEvent {
  type: "log";
  ts: number;
  source: string;
  level: string;
  message: string;
  job?: string;
}

export interface ProgressEvent {
  type: "progress";
  ts: number;
  source?: string;
  job?: string;
  stage: string;
  percent?: number | null;
  message?: string | null;
  bytes?: ProgressBytes | null;
}

export interface StatusEvent {
  type: "status";
  ts: number;
  model: string;
  state: string;
  message?: string | null;
}

export interface EnvEvent {
  type: "env";
  ts: number;
  state: string;
  message?: string | null;
}

export interface JobEvent {
  type: "job";
  ts: number;
  data: Job;
}

/** core.Op 是后端正在跑的后台任务（装依赖 / 下权重 / 重建 venv）。 */
export interface Op {
  kind: string;
  model?: string;
  label: string;
  started: number;
}

/** 一条模型的后台进度：百分比 + 一句说明。 */
export interface ModelProgress {
  percent: number | null;
  note: string;
}

export type AppEvent = LogEvent | ProgressEvent | StatusEvent | EnvEvent | JobEvent;
