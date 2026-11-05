import { useState } from "react";
import { Link } from "@tanstack/react-router";
import type { User } from "../lib/types";
import { query } from "../lib/api";
import { date } from "../lib/format";
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
import { AdminCreate } from "./orders";

function PermissionDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const w = useWrite(onClose);
  const enabled = !user.invoice_enabled;
  return (
    <Modal
      title={enabled ? "开通自助开票权限" : "撤销自助开票权限"}
      onClose={onClose}
      locked={w.locked}
    >
      <p className="context">
        <strong>
          {user.display_name || user.username} · ID {user.id}
        </strong>
        <span>{user.email || "未记录邮箱"}</span>
      </p>
      <Notice>
        {enabled
          ? "开通后，用户可以维护企业抬头并自助申请开票。"
          : "撤销后不能新建自助申请或维护抬头；历史申请和文件仍可访问，待处理申请继续处理。"}
      </Notice>
      <form
        method="post"
        onSubmit={(e) => {
          e.preventDefault();
          w.run(`/admin/users/${user.id}/permission`, "PUT", {
            enabled,
            expected_version: user.permission_version,
            remark: String(new FormData(e.currentTarget).get("remark") || "").trim(),
          });
        }}
      >
        <fieldset disabled={w.locked}>
          <Field label="操作备注" hint="选填，最多 512 字，写入操作记录。">
            <textarea name="remark" rows={3} maxLength={512} />
          </Field>
        </fieldset>
        {w.feedback}
        <footer>
          <button type="button" onClick={onClose} disabled={w.locked}>
            取消
          </button>
          <Submit pending={w.pending} disabled={w.locked}>
            {enabled ? "确认开通" : "确认撤销"}
          </Submit>
        </footer>
      </form>
    </Modal>
  );
}
export function UsersPage() {
  const [keyword, setKeyword] = useState(""),
    [id, setID] = useState(""),
    [enabled, setEnabled] = useState(""),
    [account, setAccount] = useState(""),
    [page, setPage] = useState(1),
    [permission, setPermission] = useState<User | null>(null),
    [target, setTarget] = useState<User | null>(null),
    [success, setSuccess] = useState(false);
  const q = useList<User>(
    "/admin/users" +
      query({ page, keyword, user_id: id, invoice_enabled: enabled, account_status: account }),
  );
  return (
    <>
      <PageHead
        eyebrow="管理工作区"
        title="用户与开票权限"
        description="检索原站用户，逐用户管理自助开票权限，或为用户代办。"
      />
      {success && <Notice>代办申请已提交。</Notice>}
      <section className="panel">
        <div className="toolbar">
          <SearchBox
            value={keyword}
            onChange={(v) => {
              setKeyword(v);
              setPage(1);
            }}
            placeholder="用户名 / 显示名 / 邮箱"
          />
          <SearchBox
            label="精确用户 ID"
            value={id}
            onChange={(v) => {
              setID(v);
              setPage(1);
            }}
            placeholder="精确用户 ID"
          />
          <label className="inline-field">
            开票权限
            <select
              value={enabled}
              onChange={(e) => {
                setEnabled(e.target.value);
                setPage(1);
              }}
            >
              <option value="">全部</option>
              <option value="true">已开通</option>
              <option value="false">未开通</option>
            </select>
          </label>
          <label className="inline-field">
            账号状态
            <select
              value={account}
              onChange={(e) => {
                setAccount(e.target.value);
                setPage(1);
              }}
            >
              <option value="">全部</option>
              <option value="enabled">正常</option>
              <option value="disabled">已禁用</option>
            </select>
          </label>
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
                  <th>用户</th>
                  <th>账号状态</th>
                  <th>自助开票</th>
                  <th>权限更新时间</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {q.data.items.map((u) => (
                  <tr key={u.id}>
                    <td>
                      <strong>{u.display_name || u.username}</strong>
                      <small>
                        @{u.username} · ID {u.id}
                      </small>
                      <small>{u.email || "未记录邮箱"}</small>
                    </td>
                    <td>
                      <span
                        className={`badge ${u.account_status === "enabled" ? "invoiced" : "cancelled"}`}
                      >
                        {u.account_status === "enabled" ? "正常" : "已禁用"}
                      </span>
                    </td>
                    <td>
                      <span className={`badge ${u.invoice_enabled ? "invoiced" : "cancelled"}`}>
                        {u.invoice_enabled ? "已开通" : "未开通"}
                      </span>
                    </td>
                    <td>{date(u.permission_changed_at)}</td>
                    <td>
                      <div className="actions">
                        <button
                          disabled={!u.can_change_permission}
                          onClick={() => setPermission(u)}
                        >
                          {u.invoice_enabled ? "撤销权限" : "开通权限"}
                        </button>
                        <button disabled={!u.can_create_application} onClick={() => setTarget(u)}>
                          代办开票
                        </button>
                        <Link to="/admin/applications" search={{ user_id: u.id }}>
                          申请记录
                        </Link>
                        <Link to="/admin/operations" search={{ target_user_id: u.id }}>
                          操作记录
                        </Link>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <Pager data={q.data} page={page} onChange={setPage} />
      </section>
      {permission && (
        <PermissionDialog
          key={permission.id + permission.permission_version}
          user={permission}
          onClose={() => setPermission(null)}
        />
      )}
      {target && (
        <AdminCreate
          initial={target}
          onClose={() => setTarget(null)}
          onSuccess={() => setSuccess(true)}
        />
      )}
    </>
  );
}
