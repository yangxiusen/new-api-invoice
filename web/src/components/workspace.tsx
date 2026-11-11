import { useEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { Link, Outlet, useLocation } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Building2,
  ClipboardList,
  FileCheck2,
  History,
  LayoutGrid,
  LogOut,
  Menu,
  ReceiptText,
  Users,
  X,
  ArrowUpRight,
} from "lucide-react";
import { auth } from "../lib/auth";
import { get, message } from "../lib/api";
import type { Me } from "../lib/types";
import { Notice } from "./ui";

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: ({ signal }) => get<Me>("/me", signal),
    retry: false,
  });
}
function LoginPage({ unavailable = false }: { unavailable?: boolean }) {
  const session = useSyncExternalStore(auth.subscribe, auth.getSnapshot);
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const resume = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await auth.refresh();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="login-page">
      <section>
        <div className="brand-mark">
          <ReceiptText size={28} />
        </div>
        <span className="eyebrow">INVOICE</span>
        <h1>{unavailable ? "连接暂不可用" : "进入发票工作台"}</h1>
        <p>使用原站账号登录，管理企业抬头、开票申请和发票文件。</p>
        {(unavailable || error) && (
          <Notice error>{error || session.error || "发票或原站服务暂不可用，请稍后重试。"}</Notice>
        )}
        <button
          className="primary"
          disabled={busy}
          onClick={() => window.open("/sign-in", "_blank", "noopener,noreferrer")}
        >
          前往原站登录 <ArrowUpRight size={17} />
        </button>
        <button disabled={busy} onClick={() => void resume()}>
          {busy ? "正在确认登录…" : "我已完成登录，继续"}
        </button>
        <small>登录页面会在新标签页打开，完成后返回此页。未开通用户仍可查看历史申请。</small>
      </section>
    </main>
  );
}
export function Workspace() {
  const session = useSyncExternalStore(auth.subscribe, auth.getSnapshot),
    client = useQueryClient(),
    location = useLocation();
  const [menu, setMenu] = useState(false),
    [logoutError, setLogoutError] = useState(""),
    [loggingOut, setLoggingOut] = useState(false);
  const [mobile, setMobile] = useState(() => window.matchMedia("(max-width:760px)").matches);
  useEffect(() => {
    const media = window.matchMedia("(max-width:760px)");
    const change = () => setMobile(media.matches);
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  useEffect(() => {
    if (!menu) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenu(false);
    };
    window.addEventListener("keydown", escape);
    return () => window.removeEventListener("keydown", escape);
  }, [menu]);
  useEffect(() => {
    const disconnect = auth.connect();
    void auth.refresh().catch(() => undefined);
    let lastFocus = 0;
    const focus = () => {
      if (Date.now() - lastFocus < 1500) return;
      lastFocus = Date.now();
      void auth.refresh().catch(() => undefined);
    };
    window.addEventListener("focus", focus);
    return () => {
      disconnect();
      window.removeEventListener("focus", focus);
    };
  }, []);
  useEffect(() => {
    client.clear();
  }, [session.revision, client]);
  useEffect(() => {
    setMenu(false);
  }, [location.pathname]);
  const q = useQuery({
    queryKey: ["me", session.revision],
    queryFn: ({ signal }) => get<Me>("/me", signal),
    enabled: session.phase === "authenticated",
    retry: false,
  });
  if (session.phase === "checking")
    return (
      <div className="boot" role="status">
        正在连接原站身份…
      </div>
    );
  if (session.phase === "anonymous") return <LoginPage />;
  if (session.phase === "unavailable") return <LoginPage unavailable />;
  if (q.isPending)
    return (
      <div className="boot" role="status">
        正在加载工作区…
      </div>
    );
  if (q.error || !q.data)
    return (
      <main className="boot">
        <Notice error>{message(q.error)}</Notice>
        <button onClick={() => void q.refetch()}>重新加载</button>
        <a href="/sign-in" target="_blank" rel="noopener noreferrer">
          前往原站登录
        </a>
      </main>
    );
  const me = q.data;
  const isAdmin = location.pathname.startsWith("/admin");
  const permitted = me.capabilities.can_submit;
  const forbidden =
    (isAdmin && !me.capabilities.can_admin) ||
    (!isAdmin &&
      (location.pathname === "/orders" || location.pathname === "/titles") &&
      !permitted);
  const logout = async () => {
    setLoggingOut(true);
    setLogoutError("");
    try {
      await auth.logout();
    } catch (e) {
      setLogoutError(message(e));
    } finally {
      setLoggingOut(false);
    }
  };
  return (
    <div className="workspace">
      <a className="skip-link" href="#main-content">
        跳到主要内容
      </a>
      {menu && (
        <button className="nav-backdrop" aria-label="收起导航" onClick={() => setMenu(false)} />
      )}
      <aside className={menu ? "open" : ""} inert={mobile && !menu}>
        <Link className="brand" to="/orders">
          <span className="brand-mark">
            <ReceiptText size={23} />
          </span>
          <span>
            Invoice<small>发票工作台</small>
          </span>
        </Link>
        <nav aria-label="工作区导航">
          <div className="nav-label">个人工作区</div>
          {permitted && (
            <Nav to="/orders" icon={<LayoutGrid size={18} />}>
              申请开票
            </Nav>
          )}
          <Nav to="/applications" icon={<ClipboardList size={18} />}>
            我的申请
          </Nav>
          {permitted && (
            <Nav to="/titles" icon={<Building2 size={18} />}>
              企业抬头
            </Nav>
          )}
          {me.capabilities.can_admin && (
            <>
              <div className="nav-label">管理工作区</div>
              <Nav to="/admin/users" icon={<Users size={18} />}>
                用户与权限
              </Nav>
              <Nav to="/admin/applications" icon={<FileCheck2 size={18} />}>
                发票管理
              </Nav>
              <Nav to="/admin/operations" icon={<History size={18} />}>
                操作记录
              </Nav>
            </>
          )}
        </nav>
        <div className="sidebar-foot">
          <span className="service-dot" />
          独立发票服务<small>Invoice v0.1</small>
        </div>
      </aside>
      <div className="main-shell">
        <header className="topbar">
          <button
            className="icon mobile-menu"
            aria-label={menu ? "收起导航" : "展开导航"}
            aria-expanded={menu}
            onClick={() => setMenu(!menu)}
          >
            {menu ? <X size={22} /> : <Menu size={22} />}
          </button>
          <span className="breadcrumb">
            Invoice <span>/</span> {isAdmin ? "管理工作区" : "个人工作区"}
          </span>
          <div className="identity">
            <span className="avatar">{(me.user.display_name || me.user.username).slice(0, 1)}</span>
            <span>
              {me.user.display_name || me.user.username}
              <small>
                {me.capabilities.can_admin ? "管理员" : "用户"} · ID {me.user.id}
              </small>
            </span>
            <button aria-label="退出原站登录" disabled={loggingOut} onClick={() => void logout()}>
              <LogOut size={16} />
              <span className="desktop-label">{loggingOut ? "退出中…" : "退出"}</span>
            </button>
          </div>
        </header>
        <main id="main-content" key={session.revision}>
          {logoutError && <Notice error>{logoutError}</Notice>}
          {!permitted && !isAdmin && (
            <Notice>自助开票功能尚未开通，请联系管理员。历史申请和已开发票仍可查看、下载。</Notice>
          )}
          {forbidden ? (
            <section className="panel access">
              <h1>{isAdmin ? "需要管理员权限" : "尚未开通自助开票"}</h1>
              <p>请联系管理员确认权限，或查看已有申请。</p>
              <Link to="/applications">查看我的申请</Link>
            </section>
          ) : (
            <Outlet />
          )}
        </main>
        <footer className="page-foot">
          Invoice · 发票工作台<span>时间统一按北京时间展示</span>
        </footer>
      </div>
    </div>
  );
}
function Nav({ to, icon, children }: { to: string; icon: ReactNode; children: ReactNode }) {
  return (
    <Link to={to} activeProps={{ className: "active" }}>
      {icon}
      <span>{children}</span>
    </Link>
  );
}
