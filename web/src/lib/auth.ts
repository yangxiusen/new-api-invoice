export type SessionSnapshot = {
  phase: "checking" | "anonymous" | "authenticated" | "unavailable";
  token: string;
  sid: string;
  expiresAt: number;
  revision: number;
  error?: string;
};
type SessionEvent = {
  kind: "authenticated" | "signed_out";
  sid: string;
  timestamp: number;
  source?: string;
  nonce?: string;
};
type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type Bundle = {
  access_token: string;
  access_expires_at: number;
  token_type: string;
  session: { sid: string };
  user: { id: number };
};
const isRecord = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object";
function bundle(v: unknown): v is Bundle {
  return (
    isRecord(v) &&
    typeof v.access_token === "string" &&
    v.access_token.length > 0 &&
    v.token_type === "Bearer" &&
    typeof v.access_expires_at === "number" &&
    v.access_expires_at > Date.now() / 1000 &&
    isRecord(v.session) &&
    typeof v.session.sid === "string" &&
    v.session.sid.length > 0 &&
    isRecord(v.user) &&
    typeof v.user.id === "number" &&
    v.user.id > 0
  );
}

export class AuthSession {
  private state: SessionSnapshot = {
    phase: "checking",
    token: "",
    sid: "",
    expiresAt: 0,
    revision: 0,
  };
  private epoch = 0;
  private inflight: Promise<void> | null = null;
  private refreshEpoch = 0;
  private listeners = new Set<() => void>();
  private publish: ((e: SessionEvent) => void) | undefined;
  constructor(private fetcher: Fetcher = (...args) => fetch(...args)) {}
  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };
  private set(patch: Partial<SessionSnapshot>) {
    this.state = { ...this.state, ...patch };
    this.listeners.forEach((fn) => fn());
  }
  clear() {
    this.epoch++;
    this.set({
      phase: "anonymous",
      token: "",
      sid: "",
      expiresAt: 0,
      revision: this.state.revision + 1,
      error: undefined,
    });
  }
  unavailable() {
    this.set({ phase: "unavailable", error: "原站身份服务暂不可用，请稍后重试。" });
  }
  receive(value: unknown) {
    if (
      !isRecord(value) ||
      typeof value.sid !== "string" ||
      typeof value.timestamp !== "number" ||
      Math.abs(Date.now() - value.timestamp) > 60000
    )
      return;
    if (value.kind === "signed_out" && (!this.state.sid || value.sid === this.state.sid)) {
      this.clear();
      return;
    }
    if (value.kind === "authenticated" && value.sid !== this.state.sid) {
      this.clear();
      void this.refresh().catch(() => undefined);
    }
  }
  connect() {
    const source = crypto.randomUUID();
    let channel: BroadcastChannel | undefined;
    const deliver = (v: unknown) => {
      if (isRecord(v) && v.source !== source) this.receive(v);
    };
    if (typeof BroadcastChannel !== "undefined") {
      channel = new BroadcastChannel("new-api:auth-session");
      channel.onmessage = (e) => deliver(e.data);
    }
    const storage = (e: StorageEvent) => {
      if (e.key === "new-api:auth-session:event" && e.newValue) {
        try {
          deliver(JSON.parse(e.newValue));
        } catch {
          /* malformed event */
        }
      }
    };
    window.addEventListener("storage", storage);
    this.publish = (event) => {
      const e = { ...event, source, nonce: crypto.randomUUID() };
      if (channel) channel.postMessage(e);
      else {
        try {
          localStorage.setItem("new-api:auth-session:event", JSON.stringify(e));
          localStorage.removeItem("new-api:auth-session:event");
        } catch {
          /* optional synchronization */
        }
      }
    };
    return () => {
      channel?.close();
      window.removeEventListener("storage", storage);
      this.publish = undefined;
    };
  }
  refresh(): Promise<void> {
    if (this.inflight) {
      if (this.refreshEpoch !== this.epoch)
        return this.inflight.catch(() => undefined).then(() => this.refresh());
      return this.inflight;
    }
    const epoch = this.epoch;
    this.refreshEpoch = epoch;
    const run = async () => {
      for (let attempt = 0; attempt < 2; attempt++) {
        const expected = this.state.sid;
        let res: Response;
        try {
          res = await this.fetcher("/api/user/auth/refresh", {
            method: "POST",
            credentials: "same-origin",
            headers: expected ? { "X-Auth-Session": expected } : {},
            signal: AbortSignal.timeout(5000),
          });
        } catch {
          if (epoch === this.epoch) this.unavailable();
          throw new Error("身份服务连接失败，请重试");
        }
        const data: unknown = await res.json().catch(() => null);
        if (epoch !== this.epoch) throw new Error("登录状态已变化");
        if (
          res.status === 409 &&
          isRecord(data) &&
          (data.code === "AUTH_REFRESH_RACE" || data.code === "AUTH_SESSION_MISMATCH") &&
          attempt === 0
        ) {
          if (data.code === "AUTH_SESSION_MISMATCH")
            this.set({ sid: "", token: "", expiresAt: 0, revision: this.state.revision + 1 });
          await new Promise((r) => setTimeout(r, 200));
          continue;
        }
        if (res.ok && isRecord(data) && data.success === true && bundle(data.data)) {
          const b = data.data;
          const changed = this.state.sid !== b.session.sid;
          this.set({
            phase: "authenticated",
            token: b.access_token,
            sid: b.session.sid,
            expiresAt: b.access_expires_at,
            revision: this.state.revision + (changed ? 1 : 0),
            error: undefined,
          });
          if (changed)
            this.publish?.({ kind: "authenticated", sid: b.session.sid, timestamp: Date.now() });
          return;
        }
        if (res.status === 401 || res.status === 403) {
          this.clear();
          throw new Error("请先登录原站");
        }
        this.unavailable();
        throw new Error(res.status === 429 ? "请求频繁，请稍后重试" : "身份服务暂不可用，请重试");
      }
    };
    const locked = async () => {
      if (globalThis.navigator?.locks) {
        await navigator.locks.request("new-api:auth-refresh", { mode: "exclusive" }, async () => {
          await run();
        });
      } else {
        await run();
      }
    };
    const pending = locked().finally(() => {
      this.inflight = null;
    });
    this.inflight = pending;
    return pending;
  }
  async ensure() {
    if (this.state.token && this.state.expiresAt > Date.now() / 1000 + 30) return;
    await this.refresh();
  }
  async logout() {
    const sid = this.state.sid;
    const res = await this.fetcher("/api/user/auth/logout", {
      method: "POST",
      credentials: "same-origin",
      headers: { "X-Auth-Session": sid, Authorization: `Bearer ${this.state.token}` },
      signal: AbortSignal.timeout(5000),
    });
    const data: unknown = await res.json().catch(() => null);
    if (!res.ok || !isRecord(data) || data.success !== true)
      throw new Error("原站退出未确认，请重试");
    this.clear();
    if (sid) this.publish?.({ kind: "signed_out", sid, timestamp: Date.now() });
  }
}
export const auth = new AuthSession();
