import { useState, useEffect } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Plus, Check } from "lucide-react";
import { get, query } from "../lib/api";
import { auth } from "../lib/auth";
import { checkSelection } from "../lib/orders";
import { amount, date, sumCents } from "../lib/format";
import type { Order, Title, User, Page, WriteResult } from "../lib/types";
import {
  DataState,
  Field,
  Modal,
  Notice,
  PageHead,
  Pager,
  SearchBox,
  Submit,
  useList,
  useWrite,
} from "../components/ui";
import { TitleForm } from "./titles";

export type Selection = Map<string, Order>;
export function OrderPicker({
  target,
  selected,
  onSelect,
}: {
  target?: User;
  selected: Selection;
  onSelect: (v: Selection) => void;
}) {
  const [keyword, setKeyword] = useState(""),
    [page, setPage] = useState(1),
    [limitError, setLimitError] = useState(false);
  const q = useList<Order>(
    (target ? `/admin/users/${target.id}` : "") + "/orders/eligible" + query({ keyword, page }),
  );
  const choose = (orders: Order[], checked: boolean) => {
    const next = new Map(selected);
    for (const order of orders) {
      if (checked) next.set(order.id, order);
      else next.delete(order.id);
    }
    if (next.size > 100) {
      setLimitError(true);
      return;
    }
    setLimitError(false);
    onSelect(next);
  };
  return (
    <>
      <div className="toolbar">
        <SearchBox
          value={keyword}
          onChange={(v) => {
            setKeyword(v);
            setPage(1);
          }}
          placeholder="检索充值订单号"
        />
        <span className="muted">成功充值 · 整笔申请</span>
      </div>
      {limitError && <Notice error>一次最多合并 100 笔订单，请减少选择。</Notice>}
      <DataState
        pending={q.isPending}
        error={q.error}
        empty={!q.data?.items.length}
        retry={q.refetch}
      />
      {q.data && !!q.data.items.length && (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th className="check">
                  <input
                    type="checkbox"
                    aria-label="选择本页全部订单"
                    checked={q.data.items.every((o) => selected.has(o.id))}
                    onChange={(e) => choose(q.data!.items, e.target.checked)}
                  />
                </th>
                <th>充值订单号</th>
                <th>支付方式</th>
                <th>付款时间（北京时间）</th>
                <th className="number">可开票金额</th>
              </tr>
            </thead>
            <tbody>
              {q.data.items.map((o) => (
                <tr key={o.id} className={selected.has(o.id) ? "selected" : ""}>
                  <td className="check">
                    <input
                      aria-label={`选择订单 ${o.trade_no}`}
                      type="checkbox"
                      checked={selected.has(o.id)}
                      onChange={(e) => choose([o], e.target.checked)}
                    />
                  </td>
                  <td className="mono">
                    {o.trade_no || "—"}
                    <small>ID {o.id}</small>
                  </td>
                  <td>
                    {o.payment_method || "—"}
                    <small>{o.payment_provider || "—"}</small>
                  </td>
                  <td>
                    {date(o.payment_time)}
                    {o.payment_time_source === "create_fallback" && (
                      <small className="muted">采用订单创建时间</small>
                    )}
                  </td>
                  <td className="number amount">{amount(o.amount_cents)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pager data={q.data} page={page} onChange={setPage} />
    </>
  );
}
function SelectionBar({
  selected,
  onClear,
  onNext,
  label = "下一步：选择抬头",
}: {
  selected: Selection;
  onClear: () => void;
  onNext: () => void;
  label?: string;
}) {
  return (
    <div className="selection-bar">
      <div>
        <strong>已选 {selected.size} 笔</strong>
        <button className="text" disabled={!selected.size} onClick={onClear}>
          清空选择
        </button>
        <span className="muted">跨页选择会保留</span>
      </div>
      <div>
        <span>
          合计{" "}
          <b className="total">
            {amount(sumCents([...selected.values()].map((o) => o.amount_cents)))}
          </b>
        </span>
        <button className="primary" disabled={!selected.size} onClick={onNext}>
          {label}
        </button>
      </div>
    </div>
  );
}
export function ReviewForm({
  selected,
  target,
  onSuccess,
  onBack,
  onLock,
}: {
  selected: Selection;
  target?: User;
  onSuccess: (r: WriteResult) => void;
  onBack: () => void;
  onLock?: (locked: boolean) => void;
}) {
  const [title, setTitle] = useState(""),
    [adding, setAdding] = useState(false),
    [verifying, setVerifying] = useState(false),
    [verificationError, setVerificationError] = useState("");
  const w = useWrite(onSuccess);
  const path = target ? `/admin/users/${target.id}/titles` : "/titles";
  const q = useInfiniteQuery({
    queryKey: ["picker-titles", path],
    initialPageParam: 1,
    queryFn: ({ pageParam, signal }) =>
      get<Page<Title>>(path + query({ page: pageParam, page_size: 100 }), signal),
    getNextPageParam: (last) =>
      last.page * last.page_size < last.total ? last.page + 1 : undefined,
    retry: false,
  });
  const titles = q.data?.pages.flatMap((p) => p.items) || [];
  const current = titles.find((t) => t.id === title);
  // Parent owns the outer dialog; lock it while the transaction outcome is unresolved.
  const locked = w.locked || verifying;
  useEffect(() => {
    onLock?.(locked);
  }, [locked, onLock]);
  return (
    <form
      method="post"
      onSubmit={async (e) => {
        e.preventDefault();
        if (locked) return;
        const remark = String(new FormData(e.currentTarget).get("user_remark") || "").trim();
        const revision = auth.getSnapshot().revision;
        setVerifying(true);
        setVerificationError("");
        try {
          const fresh = await get<Page<Order>>(
            (target ? `/admin/users/${target.id}` : "") +
              "/orders/eligible" +
              query({ topup_ids: [...selected.keys()].join(","), page_size: 100 }),
          );
          const expected = checkSelection([...selected.values()], fresh.items);
          if (auth.getSnapshot().revision !== revision)
            throw new Error("登录身份已变化，请重新操作");
          w.run(target ? "/admin/applications" : "/applications", "POST", {
            ...(target ? { user_id: target.id } : {}),
            title_id: title,
            topup_ids: [...selected.keys()],
            expected_order_amounts: expected,
            user_remark: remark,
          });
        } catch (error) {
          setVerificationError(error instanceof Error ? error.message : "订单重核失败，请重试");
        } finally {
          setVerifying(false);
        }
      }}
    >
      {target && (
        <Notice>
          正在为 <strong>{target.display_name || target.username}</strong>（ID {target.id}，
          {target.email || "未记录邮箱"}）代办。此操作不会开通用户自助权限。
        </Notice>
      )}
      <div className="summary-card">
        <span>合并 {selected.size} 笔充值订单</span>
        <strong>{amount(sumCents([...selected.values()].map((o) => o.amount_cents)))}</strong>
        <small>人民币 · 整笔金额</small>
      </div>
      <details className="order-review">
        <summary>查看已选订单</summary>
        <ul>
          {[...selected.values()].map((o) => (
            <li key={o.id}>
              <span className="mono">{o.trade_no}</span>
              <b>{amount(o.amount_cents)}</b>
            </li>
          ))}
        </ul>
      </details>
      <fieldset disabled={locked}>
        <Field label="企业抬头" required>
          <select required value={title} onChange={(e) => setTitle(e.target.value)}>
            <option value="">请选择企业抬头</option>
            {titles.map((t) => (
              <option key={t.id} value={t.id}>
                {t.company_name} · {t.tax_no}
              </option>
            ))}
          </select>
        </Field>
        <div className="actions">
          <button type="button" onClick={() => setAdding(true)}>
            <Plus size={16} />
            新增抬头
          </button>
          {q.hasNextPage && (
            <button
              type="button"
              disabled={q.isFetchingNextPage}
              onClick={() => void q.fetchNextPage()}
            >
              加载更多抬头
            </button>
          )}
        </div>
        {q.error && (
          <Notice error>
            抬头加载失败，请
            <button type="button" onClick={() => void q.refetch()}>
              重新加载
            </button>
          </Notice>
        )}
        {!q.isPending && !q.error && !titles.length && (
          <p className="muted">还没有抬头，请先新增企业抬头。</p>
        )}
        {current && (
          <div className="title-preview">
            <strong>{current.company_name}</strong>
            <span>税号：{current.tax_no}</span>
            <span>接收邮箱：{current.email}</span>
          </div>
        )}
        <Field label="申请备注" hint="选填，最多 512 字；管理员处理时可见。">
          <textarea name="user_remark" rows={3} maxLength={512} />
        </Field>
      </fieldset>
      {verificationError && <Notice error>{verificationError}</Notice>}
      {w.feedback}
      <footer>
        <button type="button" disabled={locked} onClick={onBack}>
          返回选单
        </button>
        <Submit pending={w.pending} disabled={locked || !current}>
          确认提交申请
        </Submit>
      </footer>
      {adding && (
        <TitleForm
          target={
            target ? { id: target.id, name: target.display_name || target.username } : undefined
          }
          onClose={() => setAdding(false)}
          onSaved={(id) => {
            setTitle(id);
            void q.refetch();
          }}
        />
      )}
    </form>
  );
}
export function OrdersPage() {
  const [selected, setSelected] = useState<Selection>(new Map()),
    [review, setReview] = useState(false),
    [success, setSuccess] = useState<WriteResult | null>(null);
  return (
    <>
      <PageHead
        eyebrow="个人工作区"
        title="申请开票"
        description="选择已支付的充值订单，合并申请企业发票。"
      />
      {success && (
        <Notice>
          <Check size={16} />
          申请 {success.request_no} 已提交，可到“我的申请”查看进度。
        </Notice>
      )}
      <Notice>
        仅列出未被占用的成功充值订单，每笔订单按实际付款金额整笔申请，最多合并 100 笔。
      </Notice>
      <section className="panel">
        <OrderPicker selected={selected} onSelect={setSelected} />
        <SelectionBar
          selected={selected}
          onClear={() => setSelected(new Map())}
          onNext={() => setReview(true)}
        />
      </section>
      {review && (
        <ReviewDialog
          selected={selected}
          onClose={() => setReview(false)}
          onSuccess={(r) => {
            setSuccess(r);
            setReview(false);
            setSelected(new Map());
          }}
        />
      )}
    </>
  );
}
function ReviewDialog({
  selected,
  onClose,
  onSuccess,
}: {
  selected: Selection;
  onClose: () => void;
  onSuccess: (r: WriteResult) => void;
}) {
  const [locked, setLocked] = useState(false);
  return (
    <Modal title="确认开票申请" wide onClose={onClose} locked={locked}>
      <ReviewForm selected={selected} onBack={onClose} onSuccess={onSuccess} onLock={setLocked} />
    </Modal>
  );
}
export function UserPicker({ onPick }: { onPick: (u: User) => void }) {
  const [keyword, setKeyword] = useState(""),
    [userID, setUserID] = useState(""),
    [page, setPage] = useState(1);
  const q = useList<User>(
    "/admin/users" + query({ keyword, user_id: userID, page, account_status: "enabled" }),
  );
  return (
    <>
      <p className="muted">先检索并确认代办对象，仅正常账号可创建申请。</p>
      <div className="toolbar">
        <SearchBox
          value={keyword}
          onChange={(v) => {
            setKeyword(v);
            setPage(1);
          }}
          placeholder="用户名 / 邮箱 / 显示名"
        />
        <SearchBox
          label="精确用户 ID"
          value={userID}
          onChange={(v) => {
            setUserID(v);
            setPage(1);
          }}
          placeholder="精确用户 ID"
        />
      </div>
      <DataState
        pending={q.isPending}
        error={q.error}
        empty={!q.data?.items.length}
        retry={q.refetch}
      />
      {q.data && (
        <div className="user-picker">
          {q.data.items.map((u) => (
            <button key={u.id} type="button" onClick={() => onPick(u)}>
              <span>
                <strong>{u.display_name || u.username}</strong>
                <small>
                  @{u.username} · {u.email || "未记录邮箱"}
                </small>
              </span>
              <span>ID {u.id} →</span>
            </button>
          ))}
        </div>
      )}
      <Pager data={q.data} page={page} onChange={setPage} />
    </>
  );
}
export function AdminCreate({
  initial,
  onClose,
  onSuccess,
}: {
  initial?: User;
  onClose: () => void;
  onSuccess: () => void;
}) {
  const [target, setTarget] = useState<User | undefined>(initial),
    [step, setStep] = useState(initial ? 2 : 1),
    [selected, setSelected] = useState<Selection>(new Map()),
    [locked, setLocked] = useState(false);
  return (
    <Modal title="为用户代办开票" wide locked={locked} onClose={onClose}>
      <ol className="steps">
        {["选择用户", "选择订单", "确认申请"].map((s, i) => (
          <li key={s} className={step === i + 1 ? "active" : ""}>
            <span>{i + 1}</span>
            {s}
          </li>
        ))}
      </ol>
      {step === 1 && (
        <UserPicker
          onPick={(u) => {
            setTarget(u);
            setSelected(new Map());
            setStep(2);
          }}
        />
      )}
      {step === 2 && target && (
        <>
          <div className="context">
            <strong>
              {target.display_name || target.username} · ID {target.id}
            </strong>
            <span>{target.email}</span>
            <button
              onClick={() => {
                setStep(1);
                setSelected(new Map());
                setTarget(undefined);
              }}
            >
              更换用户
            </button>
          </div>
          <OrderPicker key={target.id} target={target} selected={selected} onSelect={setSelected} />
          <SelectionBar
            selected={selected}
            onClear={() => setSelected(new Map())}
            onNext={() => setStep(3)}
          />
        </>
      )}
      {step === 3 && target && (
        <ReviewForm
          key={target.id}
          target={target}
          selected={selected}
          onBack={() => setStep(2)}
          onLock={setLocked}
          onSuccess={() => {
            onSuccess();
            onClose();
          }}
        />
      )}
    </Modal>
  );
}
