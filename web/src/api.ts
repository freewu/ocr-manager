interface EnvBody {
  env: EnvStatus;
  python: string;
  venv: string;
  base?: string | null;
  detected?: string | null;
}

interface LayoutBody {
  layout: { models_dir?: string | null; out_dir?: string | null };
  current: Record<string, string>;
}

interface HistoryDetail {
  entry: HistoryEntry;
  result: OcrResult;
}

import type {
  AppEvent,
  AppInfo,
  EnvStatus,
  HistoryEntry,
  Job,
  ModelInfo,
  OcrResult,
  Settings,
} from "./types";

const BASE = import.meta.env.VITE_API_BASE ?? "";

async function toJson<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      message = body?.error ?? message;
    } catch {
      /* 不是 JSON 就用状态码 */
    }
    throw new Error(message);
  }
  return (await res.json()) as T;
}

function jsonPost(path: string, body?: unknown, method = "POST"): Promise<Response> {
  return fetch(`${BASE}${path}`, {
    method,
    headers: { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export const api = {
  info(): Promise<AppInfo> {
    return fetch(`${BASE}/api/info`).then(toJson<AppInfo>);
  },
  models(refresh = false): Promise<ModelInfo[]> {
    return fetch(`${BASE}/api/models?refresh=${refresh}`)
      .then(toJson<{ models: ModelInfo[] }>)
      .then((body) => body.models ?? []);
  },
  prepare(id: string, force = false): Promise<unknown> {
    return fetch(`${BASE}/api/models/${id}/prepare?force=${force ? 1 : 0}`, {
      method: "POST",
    }).then(toJson);
  },
  activate(id: string): Promise<unknown> {
    return fetch(`${BASE}/api/models/${id}/activate`, { method: "POST" }).then(toJson);
  },
  deactivate(id: string): Promise<unknown> {
    return fetch(`${BASE}/api/models/${id}/deactivate`, { method: "POST" }).then(toJson);
  },

  env(): Promise<EnvBody> {
    return fetch(`${BASE}/api/env`).then((res) => toJson<EnvBody>(res));
  },
  installEnv(extras: string[] = []): Promise<unknown> {
    return jsonPost("/api/env/install", { extras }).then(toJson);
  },
  recreateEnv(): Promise<unknown> {
    return jsonPost("/api/env/recreate").then(toJson);
  },
  selectBasePython(base: string | null): Promise<unknown> {
    return jsonPost("/api/env/base", { base }).then(toJson);
  },

  settings(): Promise<Settings> {
    return fetch(`${BASE}/api/settings`)
      .then(toJson<{ settings: Settings }>)
      .then((body) => body.settings);
  },
  saveSettings(patch: Partial<Settings>): Promise<Settings> {
    return jsonPost("/api/settings", patch)
      .then(toJson<{ settings: Settings }>)
      .then((body) => body.settings);
  },
  layout(): Promise<LayoutBody> {
    return fetch(`${BASE}/api/layout`).then((res) => toJson<LayoutBody>(res));
  },
  saveLayout(patch: { models_dir?: string | null; out_dir?: string | null }): Promise<unknown> {
    return jsonPost("/api/layout", patch).then(toJson);
  },

  recognize(files: File[], model: string, options: Record<string, unknown>): Promise<Job> {
    const form = new FormData();
    form.append("model", model);
    form.append("options", JSON.stringify(options));
    for (const file of files) form.append("files", file, file.name);
    return fetch(`${BASE}/api/recognize`, { method: "POST", body: form })
      .then(toJson<{ job: Job }>)
      .then((body) => body.job);
  },
  job(id: string): Promise<Job> {
    return fetch(`${BASE}/api/jobs/${id}`)
      .then(toJson<{ job: Job }>)
      .then((body) => body.job);
  },
  cancelJob(id: string): Promise<unknown> {
    return jsonPost(`/api/jobs/${id}/cancel`).then(toJson);
  },

  history(limit = 100): Promise<HistoryEntry[]> {
    return fetch(`${BASE}/api/history?limit=${limit}`)
      .then(toJson<{ items: HistoryEntry[] }>)
      .then((body) => body.items ?? []);
  },
  historyItem(id: string): Promise<HistoryDetail> {
    return fetch(`${BASE}/api/history/${id}`).then((res) => toJson<HistoryDetail>(res));
  },
  deleteHistory(id: string): Promise<unknown> {
    return fetch(`${BASE}/api/history/${id}`, { method: "DELETE" }).then(toJson);
  },
  fileUrl(id: string, name: string, download = false): string {
    return `${BASE}/api/history/${id}/files/${name}${download ? "?download=1" : ""}`;
  },

  logs(n = 300): Promise<AppEvent[]> {
    return fetch(`${BASE}/api/logs?n=${n}`)
      .then(toJson<{ events: AppEvent[] }>)
      .then((body) => body.events ?? []);
  },
};

/** 订阅 SSE 事件流，返回取消函数。 */
export function subscribeEvents(onEvent: (ev: AppEvent) => void): () => void {
  const source = new EventSource(`${BASE}/api/events`);
  source.onmessage = (e: MessageEvent) => {
    try {
      onEvent(JSON.parse(e.data) as AppEvent);
    } catch {
      /* 忽略坏事件 */
    }
  };
  source.onerror = () => {
    /* 浏览器会自动重连 */
  };
  return () => source.close();
}
