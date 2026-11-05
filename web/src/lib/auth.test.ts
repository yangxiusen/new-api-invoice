import { describe, it, expect } from "vitest";
import { AuthSession } from "./auth";

const bundle = (sid = "one") =>
  new Response(
    JSON.stringify({
      success: true,
      data: {
        access_token: "jwt",
        token_type: "Bearer",
        access_expires_at: Date.now() / 1000 + 900,
        session: { sid },
        user: { id: 2 },
      },
    }),
    { status: 200 },
  );
describe("session isolation", () => {
  it("coalesces refreshes and retains state on dependency failure", async () => {
    let calls = 0;
    const auth = new AuthSession(async () => {
      calls++;
      return bundle();
    });
    await Promise.all([auth.refresh(), auth.refresh()]);
    expect(calls).toBe(1);
    const revision = auth.getSnapshot().revision;
    auth.receive({ kind: "authenticated", sid: "one", timestamp: Date.now() });
    expect(auth.getSnapshot().revision).toBe(revision);
    auth.receive({ kind: "signed_out", sid: "one", timestamp: Date.now() });
    expect(auth.getSnapshot().phase).toBe("anonymous");
    expect(auth.getSnapshot().token).toBe("");
  });
  it("does not treat 503 as logout", async () => {
    let fail = false;
    const auth = new AuthSession(async () =>
      fail ? new Response("{}", { status: 503 }) : bundle(),
    );
    await auth.refresh();
    fail = true;
    await expect(auth.refresh()).rejects.toThrow();
    expect(auth.getSnapshot().sid).toBe("one");
  });
  it("drops a refresh response superseded by signout", async () => {
    let resolve!: (r: Response) => void;
    const auth = new AuthSession(
      () =>
        new Promise((r) => {
          resolve = r;
        }),
    );
    const promise = auth.refresh();
    await Promise.resolve();
    auth.clear();
    resolve(bundle());
    await expect(promise).rejects.toThrow();
    expect(auth.getSnapshot().token).toBe("");
  });
  it("refreshes again when a new login supersedes an in-flight refresh", async () => {
    let release!: (r: Response) => void;
    let calls = 0;
    const auth = new AuthSession(() => {
      calls++;
      return calls === 1
        ? new Promise((r) => {
            release = r;
          })
        : Promise.resolve(bundle("new"));
    });
    const first = auth.refresh();
    await Promise.resolve();
    auth.clear();
    const next = auth.refresh();
    release(bundle("old"));
    await first.catch(() => undefined);
    await next;
    expect(auth.getSnapshot().sid).toBe("new");
    expect(calls).toBe(2);
  });
});
