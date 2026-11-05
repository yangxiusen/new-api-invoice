import { useState } from "react";
import { Plus } from "lucide-react";
import { query } from "../lib/api";
import type { Title, TitleInput } from "../lib/types";
import { date } from "../lib/format";
import {
  DataState,
  Field,
  Modal,
  PageHead,
  Pager,
  SearchBox,
  Submit,
  useList,
  useWrite,
} from "../components/ui";

export function TitleForm({
  title,
  onClose,
  onSaved,
  target,
}: {
  title?: Title;
  onClose: () => void;
  onSaved?: (id: string) => void;
  target?: { id: string; name: string };
}) {
  const w = useWrite((r) => {
    onSaved?.(r.resource_id);
    onClose();
  });
  const fields: {
    key: keyof TitleInput;
    label: string;
    required?: boolean;
    max: number;
    type?: string;
  }[] = [
    { key: "company_name", label: "企业名称", required: true, max: 255 },
    { key: "tax_no", label: "纳税人识别号", required: true, max: 64 },
    { key: "email", label: "接收邮箱", required: true, max: 255, type: "email" },
    { key: "registered_address", label: "注册地址", max: 512 },
    { key: "registered_phone", label: "注册电话", max: 64 },
    { key: "bank_name", label: "开户银行", max: 255 },
    { key: "bank_account", label: "银行账号", max: 128 },
  ];
  return (
    <Modal
      title={title ? "编辑企业抬头" : target ? "为用户新增抬头" : "新增企业抬头"}
      onClose={onClose}
      locked={w.locked}
    >
      {target && (
        <p className="context">
          当前用户：{target.name} · ID {target.id}
        </p>
      )}
      <form
        method="post"
        onSubmit={(e) => {
          e.preventDefault();
          const form = new FormData(e.currentTarget);
          const input = Object.fromEntries(
            fields.map((f) => [f.key, String(form.get(f.key) || "").trim()]),
          ) as TitleInput;
          w.run(
            target ? `/admin/users/${target.id}/titles` : title ? `/titles/${title.id}` : "/titles",
            title ? "PUT" : "POST",
            input,
          );
        }}
      >
        <fieldset disabled={w.locked}>
          <div className="form-grid">
            {fields.map((f) => (
              <Field key={f.key} label={f.label} required={f.required}>
                <input
                  name={f.key}
                  type={f.type || "text"}
                  required={f.required}
                  maxLength={f.max}
                  defaultValue={title?.[f.key] || ""}
                />
              </Field>
            ))}
          </div>
        </fieldset>
        {w.feedback}
        <footer>
          <button type="button" onClick={onClose} disabled={w.locked}>
            取消
          </button>
          <Submit pending={w.pending} disabled={w.locked && !w.pending}>
            保存抬头
          </Submit>
        </footer>
      </form>
    </Modal>
  );
}
export function TitlesPage() {
  const [keyword, setKeyword] = useState(""),
    [page, setPage] = useState(1),
    [editing, setEditing] = useState<Title | "new" | null>(null),
    [removing, setRemoving] = useState<Title | null>(null);
  const q = useList<Title>("/titles" + query({ keyword, page }));
  const w = useWrite(() => setRemoving(null));
  return (
    <>
      <PageHead
        eyebrow="个人工作区"
        title="企业抬头"
        description="维护常用开票资料，已提交申请中的抬头快照会保留。"
        action={
          <button className="primary" onClick={() => setEditing("new")}>
            <Plus size={17} />
            新增抬头
          </button>
        }
      />
      <section className="panel">
        <div className="toolbar">
          <SearchBox
            value={keyword}
            onChange={(v) => {
              setKeyword(v);
              setPage(1);
            }}
            placeholder="企业名称 / 纳税人识别号"
          />
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
                  <th>企业名称</th>
                  <th>纳税人识别号</th>
                  <th>接收邮箱</th>
                  <th>最近更新</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {q.data.items.map((t) => (
                  <tr key={t.id}>
                    <td>
                      <strong>{t.company_name}</strong>
                      <small>企业抬头 · ID {t.id}</small>
                    </td>
                    <td className="mono">{t.tax_no || "—"}</td>
                    <td>{t.email || "—"}</td>
                    <td>{date(t.updated_at)}</td>
                    <td>
                      <div className="actions">
                        <button onClick={() => setEditing(t)}>编辑</button>
                        <button className="danger-text" onClick={() => setRemoving(t)}>
                          删除
                        </button>
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
      {editing && (
        <TitleForm
          title={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
      {removing && (
        <Modal title="删除企业抬头" onClose={() => setRemoving(null)} locked={w.locked}>
          <p>
            确定删除 <strong>{removing.company_name}</strong>？历史申请中的抬头资料会继续保留。
          </p>
          {w.feedback}
          <footer>
            <button disabled={w.locked} onClick={() => setRemoving(null)}>
              保留
            </button>
            <button
              className="danger"
              disabled={w.locked}
              onClick={() => w.run("/titles/" + removing.id, "DELETE")}
            >
              {w.pending ? "正在删除…" : "确认删除"}
            </button>
          </footer>
        </Modal>
      )}
    </>
  );
}
