import { useEffect, useId, useRef, useState, type ReactNode, type FormEvent } from "react";
import { createPortal } from "react-dom";
import { auth } from "../lib/auth";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, FileText, Search, X } from "lucide-react";
import { get, intent, message, recover, submit, APIError, type Intent } from "../lib/api";
import type { Page, WriteResult } from "../lib/types";
import { states } from "../lib/format";

export function Notice({ children, error = false }: { children: ReactNode; error?: boolean }) {
  return (
    <div className={`notice ${error ? "error" : ""}`} role={error ? "alert" : "status"}>
      <AlertCircle size={17} />
      <div>{children}</div>
    </div>
  );
}
export function Status({ state }: { state: keyof typeof states }) {
  return (
    <span className={`badge ${state}`}>
      <span aria-hidden="true" />
      {states[state] || "历史状态"}
    </span>
  );
}
export function PageHead({
  eyebrow,
  title,
  description,
  action,
}: {
  eyebrow: string;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="page-head">
      <div>
        <span className="eyebrow">{eyebrow}</span>
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      {action}
    </div>
  );
}
export function SearchBox({
  value,
  onChange,
  label = "搜索",
  placeholder = "搜索关键词",
}: {
  value: string;
  onChange: (v: string) => void;
  label?: string;
  placeholder?: string;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return (
    <form
      className="search"
      onSubmit={(e) => {
        e.preventDefault();
        onChange(draft.trim());
      }}
    >
      <Search size={16} aria-hidden="true" />
      <input
        aria-label={label}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        placeholder={placeholder}
        maxLength={128}
      />
      <button type="submit">检索</button>
    </form>
  );
}
export function DataState({
  pending,
  error,
  empty,
  retry,
}: {
  pending: boolean;
  error: unknown;
  empty: boolean;
  retry: () => unknown;
}) {
  if (pending)
    return (
      <div className="empty" role="status">
        <div className="skeleton" />
        正在加载记录…
      </div>
    );
  if (error)
    return (
      <div className="empty">
        <Notice error>{message(error)}</Notice>
        <button onClick={() => void retry()}>重新加载</button>
      </div>
    );
  if (empty)
    return (
      <div className="empty">
        <FileText size={30} />
        <strong>暂无记录</strong>
        <p>试试调整检索条件，或稍后再来查看。</p>
      </div>
    );
  return null;
}
export function Pager({
  data,
  page,
  onChange,
}: {
  data?: Pick<Page<unknown>, "total" | "page_size">;
  page: number;
  onChange: (p: number) => void;
}) {
  if (!data) return null;
  const pages = Math.max(1, Math.ceil(data.total / data.page_size));
  return (
    <div className="pager">
      <span>
        共 {data.total} 条 · 第 {page} / {pages} 页
      </span>
      <div>
        <button disabled={page <= 1} onClick={() => onChange(page - 1)}>
          上一页
        </button>
        <button disabled={page >= pages} onClick={() => onChange(page + 1)}>
          下一页
        </button>
      </div>
    </div>
  );
}
export function Modal({
  title,
  children,
  onClose,
  wide = false,
  drawer = false,
  locked = false,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  wide?: boolean;
  drawer?: boolean;
  locked?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<Element | null>(null);
  const titleID = useId();
  useEffect(() => {
    returnFocus.current = document.activeElement;
    ref.current?.showModal();
    return () => {
      if (returnFocus.current instanceof HTMLElement) returnFocus.current.focus();
    };
  }, []);
  return createPortal(
    <dialog
      ref={ref}
      className={`${wide ? "wide" : ""} ${drawer ? "drawer" : ""}`}
      aria-labelledby={titleID}
      onSubmit={(e) => e.stopPropagation()}
      onCancel={(e) => {
        e.preventDefault();
        e.stopPropagation();
        if (!locked) onClose();
      }}
    >
      <header>
        <h2 id={titleID}>{title}</h2>
        <button
          type="button"
          className="icon"
          aria-label="关闭弹窗"
          disabled={locked}
          onClick={onClose}
        >
          <X size={20} />
        </button>
      </header>
      <div className="dialog-content">{children}</div>
    </dialog>,
    document.body,
  );
}
export function useList<T>(path: string) {
  return useQuery({
    queryKey: ["list", path],
    queryFn: ({ signal }) => get<Page<T>>(path, signal),
    retry: false,
  });
}
export function useWrite(onSuccess: (r: WriteResult) => void) {
  const [pending, setPending] = useState(false),
    [error, setError] = useState<unknown>(null),
    [unknown, setUnknown] = useState<Intent | null>(null);
  const running = useRef(false);
  const client = useQueryClient();
  const execute = async (i: Intent, check = false) => {
    if (running.current) return;
    running.current = true;
    setPending(true);
    setError(null);
    try {
      const r = check ? await recover(i) : await submit(i);
      setUnknown(null);
      await client.invalidateQueries();
      if (auth.getSnapshot().revision !== i.revision)
        throw new APIError(401, "SESSION_CHANGED", "登录身份已变化，请重新核对记录");
      onSuccess(r);
    } catch (e) {
      setError(e);
      if (e instanceof APIError && e.status >= 500) setUnknown(i);
      else if (!check) setUnknown(null);
    } finally {
      running.current = false;
      setPending(false);
    }
  };
  const run = (path: string, method: string, input?: object | FormData) => {
    if (unknown) return;
    void execute(intent(path, method, input));
  };
  const feedback = (
    <>
      {error && (
        <Notice error>
          {message(error)}
          {error instanceof APIError && error.requestID && (
            <small className="request-id">请求编号：{error.requestID}</small>
          )}
        </Notice>
      )}
      {unknown && (
        <div className="recovery">
          <p>请先确认原请求结果。重试将保留原请求键和内容。</p>
          <button disabled={pending} onClick={() => void execute(unknown, true)}>
            查询原请求结果
          </button>
          <button disabled={pending} onClick={() => void execute(unknown)}>
            原请求重试
          </button>
          <small>请求键：{unknown.key}</small>
        </div>
      )}
    </>
  );
  return { run, pending, locked: pending || !!unknown, error, feedback, unknown };
}
export function Field({
  label,
  required,
  children,
  hint,
}: {
  label: string;
  required?: boolean;
  children: ReactNode;
  hint?: string;
}) {
  return (
    <label className="field">
      <span>
        {label}
        {required && <b aria-hidden="true"> *</b>}
      </span>
      {children}
      {hint && <small>{hint}</small>}
    </label>
  );
}
export function Submit({
  pending,
  disabled,
  children = "确认提交",
}: {
  pending: boolean;
  disabled?: boolean;
  children?: ReactNode;
}) {
  return (
    <button className="primary" type="submit" disabled={pending || disabled}>
      {pending ? "正在提交…" : children}
    </button>
  );
}
export function formValues(e: FormEvent<HTMLFormElement>) {
  e.preventDefault();
  return new FormData(e.currentTarget);
}
