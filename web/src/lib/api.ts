import { auth } from "./auth";
import type { WriteResult } from "./types";
export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestID = "",
  ) {
    super(message);
  }
}
type Envelope<T> = { success: boolean; code: string; message: string; data: T; request_id: string };
const prefix = "/invoice-api/v1";
async function response<T>(res: Response): Promise<T> {
  const body = (await res.json().catch(() => null)) as Envelope<T> | null;
  if (!res.ok || !body?.success) {
    throw new APIError(
      res.status,
      body?.code || "NETWORK_ERROR",
      body?.message || "服务响应异常，请稍后重试",
      body?.request_id,
    );
  }
  return body.data;
}
export async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  await auth.ensure();
  let snapshot = auth.getSnapshot();
  const request = () =>
    fetch(prefix + path, {
      headers: { Authorization: `Bearer ${auth.getSnapshot().token}` },
      credentials: "omit",
      signal: signal
        ? AbortSignal.any([signal, AbortSignal.timeout(15000)])
        : AbortSignal.timeout(15000),
    });
  let res = await request();
  if (res.status === 401) {
    await auth.refresh();
    if (auth.getSnapshot().revision !== snapshot.revision)
      throw new APIError(401, "SESSION_CHANGED", "登录身份已变化，请重新操作");
    snapshot = auth.getSnapshot();
    res = await request();
  }
  if (auth.getSnapshot().revision !== snapshot.revision)
    throw new APIError(401, "SESSION_CHANGED", "登录身份已变化");
  return response<T>(res);
}
export type Intent = {
  key: string;
  path: string;
  method: string;
  body: string | FormData | undefined;
  revision: number;
};
export function intent(path: string, method: string, input?: object | FormData): Intent {
  return {
    key: crypto.randomUUID(),
    path,
    method,
    body: input instanceof FormData ? input : input ? JSON.stringify(input) : undefined,
    revision: auth.getSnapshot().revision,
  };
}
export async function submit(i: Intent): Promise<WriteResult> {
  await auth.ensure();
  const snap = auth.getSnapshot();
  if (snap.revision !== i.revision)
    throw new APIError(401, "SESSION_CHANGED", "登录身份已变化，请重新确认操作");
  let res: Response;
  try {
    res = await fetch(prefix + i.path, {
      method: i.method,
      body: i.body,
      credentials: "omit",
      headers: {
        Authorization: `Bearer ${snap.token}`,
        "Idempotency-Key": i.key,
        ...(typeof i.body === "string" ? { "Content-Type": "application/json" } : {}),
      },
      signal: AbortSignal.timeout(60000),
    });
  } catch {
    throw new APIError(503, "RESULT_UNKNOWN", "尚未确认写入结果，请查询结果或使用原请求重试");
  }
  if (auth.getSnapshot().revision !== i.revision)
    throw new APIError(401, "SESSION_CHANGED", "登录身份已变化，请重新核对记录");
  if (res.status === 401) {
    auth.clear();
    throw new APIError(401, "AUTH_REQUIRED", "登录已失效，请重新登录并核对原操作结果");
  }
  return response<WriteResult>(res);
}
export async function recover(i: Intent): Promise<WriteResult> {
  if (auth.getSnapshot().revision !== i.revision)
    throw new APIError(401, "SESSION_CHANGED", "请在原账号中核对操作记录");
  const data = await get<{ committed: true; result: WriteResult }>("/write-requests/" + i.key);
  return data.result;
}
export async function download(path: string, name: string) {
  await auth.ensure();
  const snap = auth.getSnapshot();
  const res = await fetch(prefix + path, {
    credentials: "omit",
    headers: { Authorization: `Bearer ${snap.token}` },
    signal: AbortSignal.timeout(60000),
  });
  if (!res.ok) {
    await response(res);
    return;
  }
  const blob = await res.blob();
  if (auth.getSnapshot().revision !== snap.revision) throw new Error("登录身份已变化，请重新下载");
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name || "invoice";
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export function message(error: unknown) {
  return error instanceof Error ? error.message : "请求失败，请重试";
}
export function query(values: Record<string, string | number | undefined>) {
  const q = new URLSearchParams();
  Object.entries(values).forEach(([k, v]) => {
    if (v !== undefined && v !== "") q.set(k, String(v));
  });
  return q.size ? "?" + q.toString() : "";
}
