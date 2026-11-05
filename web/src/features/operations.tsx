import { useState } from "react";
import { query } from "../lib/api";
import type { Operation } from "../lib/types";
import { date, operations } from "../lib/format";
import { DataState, PageHead, Pager, SearchBox, useList } from "../components/ui";
export function OperationsPage({ targetID = "" }: { targetID?: string }) {
  const [page, setPage] = useState(1),
    [keyword, setKeyword] = useState(""),
    [target, setTarget] = useState(targetID),
    [actor, setActor] = useState(""),
    [type, setType] = useState("");
  const q = useList<Operation>(
    "/admin/operations" +
      query({ page, keyword, target_user_id: target, actor_user_id: actor, operation_type: type }),
  );
  return (
    <>
      <PageHead
        eyebrow="管理工作区"
        title="操作记录"
        description="查看已提交的授权、代办及申请处理记录。"
      />
      <section className="panel">
        <div className="toolbar">
          <SearchBox
            value={keyword}
            onChange={(v) => {
              setKeyword(v);
              setPage(1);
            }}
            placeholder="操作人名称 / 申请号"
          />
          <SearchBox
            label="目标用户 ID"
            value={target}
            onChange={(v) => {
              setTarget(v);
              setPage(1);
            }}
            placeholder="目标用户 ID"
          />
          <SearchBox
            label="操作人 ID"
            value={actor}
            onChange={(v) => {
              setActor(v);
              setPage(1);
            }}
            placeholder="操作人 ID"
          />
          <label className="inline-field">
            操作类型
            <select
              value={type}
              onChange={(e) => {
                setType(e.target.value);
                setPage(1);
              }}
            >
              <option value="">全部类型</option>
              {Object.entries(operations).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
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
                  <th>时间</th>
                  <th>操作人</th>
                  <th>操作</th>
                  <th>目标用户</th>
                  <th>关联申请 / 状态</th>
                  <th>备注</th>
                </tr>
              </thead>
              <tbody>
                {q.data.items.map((o) => (
                  <tr key={o.id}>
                    <td>{date(o.occurred_at)}</td>
                    <td>
                      {o.actor_type === "system_import"
                        ? "系统 · 旧权限继承"
                        : o.actor_name || "历史未记录"}
                      <small>{o.actor_user_id ? `ID ${o.actor_user_id}` : "系统来源"}</small>
                    </td>
                    <td>
                      {operations[o.operation_type] || o.operation_type}
                      <small className="success-text">已提交</small>
                    </td>
                    <td>ID {o.target_user_id}</td>
                    <td className="mono">
                      {o.request_no || "—"}
                      <small>
                        {o.before_state || "—"} → {o.after_state || "—"}
                      </small>
                    </td>
                    <td className="wrap-text">{o.remark || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <Pager data={q.data} page={page} onChange={setPage} />
      </section>
    </>
  );
}
