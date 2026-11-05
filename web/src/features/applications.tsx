import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus, Download, FileText } from "lucide-react";
import { download, get, message, query } from "../lib/api";
import type { Application, Detail } from "../lib/types";
import { amount, date, states } from "../lib/format";
import {
  DataState,
  Field,
  Modal,
  Notice,
  PageHead,
  Pager,
  SearchBox,
  Status,
  Submit,
  useList,
  useWrite,
} from "../components/ui";
import { AdminCreate } from "./orders";

function ProcessDialog({
  detail,
  kind,
  admin,
  onClose,
  onSuccess,
}: {
  detail: Detail;
  kind: "cancel" | "reject" | "complete";
  admin: boolean;
  onClose: () => void;
  onSuccess: () => void;
}) {
  const [file, setFile] = useState<File | null>(null),
    [fileError, setFileError] = useState("");
  const w = useWrite(() => {
    onSuccess();
    onClose();
  });
  const titles = { cancel: "取消开票申请", reject: "驳回开票申请", complete: "上传发票并完成" };
  return (
    <Modal title={titles[kind]} onClose={onClose} locked={w.locked}>
      <p className="context">
        <strong>{detail.company_name}</strong>
        <span>{detail.request_no}</span>
        <b>{amount(detail.total_amount_cents)}</b>
      </p>
      <form
        method="post"
        onSubmit={(e) => {
          e.preventDefault();
          const data = new FormData(e.currentTarget);
          const base = `${admin ? "/admin" : ""}/applications/${detail.id}/${kind}`;
          if (kind === "complete") {
            if (!file || fileError) return;
            const form = new FormData();
            form.append("file", file);
            form.append("admin_remark", String(data.get("admin_remark") || ""));
            w.run(base, "POST", form);
          } else
            w.run(
              base,
              "POST",
              kind === "reject"
                ? { admin_remark: String(data.get("admin_remark") || "").trim() }
                : {},
            );
        }}
      >
        <fieldset disabled={w.locked}>
          {kind === "cancel" ? (
            <Notice>取消后，订单会释放。若仍有自助开票权限，可以重新提交申请。</Notice>
          ) : (
            <>
              <Field
                label={kind === "reject" ? "驳回原因" : "处理备注"}
                required={kind === "reject"}
                hint="用户可见，请勿填写内部敏感信息。"
              >
                <textarea
                  name="admin_remark"
                  required={kind === "reject"}
                  maxLength={512}
                  rows={3}
                />
              </Field>
              {kind === "complete" && (
                <>
                  <Field label="发票文件" required hint="仅一个 PDF/JPG/JPEG/PNG，最大 10MB。">
                    <input
                      type="file"
                      accept=".pdf,.jpg,.jpeg,.png"
                      required
                      onChange={(e) => {
                        const f = e.target.files?.[0] || null;
                        setFile(f);
                        setFileError(
                          !f
                            ? "请选择文件"
                            : f.size === 0
                              ? "文件不能为空"
                              : f.size > 10485760
                                ? "文件不能超过 10MB"
                                : !["application/pdf", "image/jpeg", "image/png"].includes(f.type)
                                  ? "文件格式不支持"
                                  : "",
                        );
                      }}
                    />
                  </Field>
                  {file && (
                    <p className="file-preview">
                      <FileText size={18} />
                      {file.name} · {(file.size / 1024).toFixed(1)} KB
                    </p>
                  )}
                  {fileError && <Notice error>{fileError}</Notice>}
                  <Notice>请核对抬头、税号及金额。完成后该申请不能覆盖上传或撤回。</Notice>
                </>
              )}
            </>
          )}
        </fieldset>
        {w.feedback}
        <footer>
          <button type="button" disabled={w.locked} onClick={onClose}>
            返回核对
          </button>
          <Submit
            pending={w.pending}
            disabled={w.locked || !!fileError || (kind === "complete" && !file)}
          >
            {kind === "complete" ? "确认完成开票" : kind === "reject" ? "确认驳回" : "确认取消"}
          </Submit>
        </footer>
      </form>
    </Modal>
  );
}
export function DetailDrawer({
  id,
  admin,
  onClose,
}: {
  id: string;
  admin: boolean;
  onClose: () => void;
}) {
  const path = `${admin ? "/admin" : ""}/applications/${id}`;
  const q = useQuery({
    queryKey: ["detail", path],
    queryFn: ({ signal }) => get<Detail>(path, signal),
    retry: false,
  });
  const [action, setAction] = useState<"cancel" | "reject" | "complete" | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null);
  const d = q.data;
  const save = async () => {
    if (!d || busy) return;
    setBusy(true);
    setError(null);
    try {
      await download(path + "/file", d.file?.name || "invoice");
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal title="开票申请详情" drawer onClose={onClose}>
      <DataState pending={q.isPending} error={q.error} empty={false} retry={q.refetch} />
      {d && (
        <>
          <div className="detail-hero">
            <span className="mono">{d.request_no}</span>
            <Status state={d.status} />
            <strong>{amount(d.total_amount_cents)}</strong>
            <small>人民币 · {d.item_count} 笔订单</small>
          </div>
          <dl className="detail-grid">
            <dt>申请时间</dt>
            <dd>{date(d.created_at)}</dd>
            <dt>所属用户</dt>
            <dd>ID {d.user_id}</dd>
            {admin && (
              <>
                <dt>申请来源</dt>
                <dd>
                  {d.creation_source === "admin"
                    ? "管理员代办"
                    : d.creation_source === "user"
                      ? "用户自助"
                      : "历史未记录"}
                  {d.operator && ` · ${d.operator.name}（ID ${d.operator.id}）`}
                </dd>
              </>
            )}
          </dl>
          <h3>企业抬头快照</h3>
          <dl className="detail-grid">
            {[
              ["企业名称", d.company_name],
              ["纳税人识别号", d.tax_no],
              ["接收邮箱", d.email],
              ["注册地址", d.registered_address],
              ["注册电话", d.registered_phone],
              ["开户银行", d.bank_name],
              ["银行账号", d.bank_account],
            ].map(([k, v]) => (
              <div className="definition-row" key={k}>
                <dt>{k}</dt>
                <dd>{v || "—"}</dd>
              </div>
            ))}
          </dl>
          <h3>充值订单</h3>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>订单号</th>
                  <th>付款时间</th>
                  <th className="number">开票金额</th>
                </tr>
              </thead>
              <tbody>
                {d.items.map((i) => (
                  <tr key={i.id}>
                    <td className="mono">
                      {i.trade_no}
                      <small>
                        {i.payment_provider} / {i.payment_method}
                      </small>
                    </td>
                    <td>
                      {date(i.topup_complete_time || i.topup_create_time)}
                      {i.payment_time_source === "create_fallback" && (
                        <small>采用订单创建时间</small>
                      )}
                    </td>
                    <td className="number">{amount(i.amount_cents)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <h3>备注与处理</h3>
          <dl className="detail-grid">
            <dt>申请备注</dt>
            <dd className="pre-wrap">{d.user_remark || "—"}</dd>
            <dt>处理备注</dt>
            <dd className="pre-wrap">{d.admin_remark || "—"}</dd>
            <dt>开票时间</dt>
            <dd>{date(d.invoiced_at)}</dd>
          </dl>
          {d.file && (
            <p className="file-preview">
              <FileText size={18} />
              {d.file.name || "历史文件未记录名称"}
            </p>
          )}
          {error && <Notice error>{message(error)}</Notice>}
          <footer>
            {d.status === "pending" &&
              (admin ? (
                <>
                  <button className="danger-text" onClick={() => setAction("reject")}>
                    驳回申请
                  </button>
                  <button className="primary" onClick={() => setAction("complete")}>
                    上传发票并完成
                  </button>
                </>
              ) : (
                <button className="danger-text" onClick={() => setAction("cancel")}>
                  取消申请
                </button>
              ))}
            {d.status === "invoiced" && (
              <button className="primary" disabled={busy} onClick={() => void save()}>
                <Download size={17} />
                {busy ? "正在下载…" : "下载发票"}
              </button>
            )}
            <button onClick={onClose}>关闭</button>
          </footer>
          {action && (
            <ProcessDialog
              detail={d}
              kind={action}
              admin={admin}
              onClose={() => setAction(null)}
              onSuccess={() => void q.refetch()}
            />
          )}
        </>
      )}
    </Modal>
  );
}
export function ApplicationsPage({
  admin = false,
  userID = "",
}: {
  admin?: boolean;
  userID?: string;
}) {
  const [page, setPage] = useState(1),
    [keyword, setKeyword] = useState(""),
    [status, setStatus] = useState(""),
    [target, setTarget] = useState(userID),
    [detail, setDetail] = useState<string | null>(null),
    [creating, setCreating] = useState(false),
    [success, setSuccess] = useState(false);
  const path =
    (admin ? "/admin" : "") +
    "/applications" +
    query({ page, keyword, status, ...(admin ? { user_id: target } : {}) });
  const q = useList<Application>(path);
  return (
    <>
      <PageHead
        eyebrow={admin ? "管理工作区" : "个人工作区"}
        title={admin ? "发票管理" : "我的申请"}
        description={
          admin
            ? "审核开票申请，上传发票并追踪处理记录。"
            : "查看申请进度、处理说明，下载已开具的发票。"
        }
        action={
          admin ? (
            <button className="primary" onClick={() => setCreating(true)}>
              <Plus size={17} />
              为用户代办
            </button>
          ) : undefined
        }
      />
      {success && <Notice>代办申请已提交，用户可在自己的申请记录中查看。</Notice>}
      <section className="panel">
        <div className="toolbar">
          <SearchBox
            value={keyword}
            onChange={(v) => {
              setKeyword(v);
              setPage(1);
            }}
            placeholder={admin ? "申请号 / 企业名称 / 订单号" : "申请号 / 企业名称"}
          />
          <label className="inline-field">
            申请状态
            <select
              value={status}
              onChange={(e) => {
                setStatus(e.target.value);
                setPage(1);
              }}
            >
              <option value="">全部状态</option>
              {Object.entries(states).map(([k, v]) => (
                <option value={k} key={k}>
                  {v}
                </option>
              ))}
            </select>
          </label>
          {admin && (
            <SearchBox
              label="所属用户 ID"
              value={target}
              onChange={(v) => {
                setTarget(v);
                setPage(1);
              }}
              placeholder="所属用户 ID"
            />
          )}
        </div>
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
                  <th>申请号 / 企业</th>
                  {admin && <th>所属用户</th>}
                  <th className="number">开票金额</th>
                  <th>状态</th>
                  <th>申请时间</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {q.data.items.map((a) => (
                  <tr key={a.id}>
                    <td>
                      <strong>{a.company_name || "历史未记录"}</strong>
                      <small className="mono">{a.request_no || `ID ${a.id}`}</small>
                    </td>
                    {admin && (
                      <td>
                        {a.display_name || a.username || "历史用户"}
                        <small>ID {a.user_id}</small>
                      </td>
                    )}
                    <td className="number amount">
                      {amount(a.total_amount_cents)}
                      <small>{a.item_count} 笔订单</small>
                    </td>
                    <td>
                      <Status state={a.status} />
                    </td>
                    <td>{date(a.created_at)}</td>
                    <td>
                      <button onClick={() => setDetail(a.id)}>
                        {a.status === "pending" && admin
                          ? "查看并处理"
                          : a.status === "invoiced"
                            ? "详情 / 下载"
                            : "查看详情"}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <Pager data={q.data} page={page} onChange={setPage} />
      </section>
      {detail && (
        <DetailDrawer key={detail} id={detail} admin={admin} onClose={() => setDetail(null)} />
      )}
      {creating && (
        <AdminCreate onClose={() => setCreating(false)} onSuccess={() => setSuccess(true)} />
      )}
    </>
  );
}
