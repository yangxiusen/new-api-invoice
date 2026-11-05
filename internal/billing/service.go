package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"invoice/internal/config"
	"invoice/internal/fault"
)

type Actor struct {
	ID             int64
	Username, Name string
	Role           int
	Status         int
	AuthVersion    int64
}

func (a Actor) Admin() bool { return a.Role == 10 || a.Role == 100 }

type Service struct {
	DB     *pgxpool.Pool
	Config config.Config
}
type Result struct {
	ResourceType      string  `json:"resource_type"`
	ResourceID        string  `json:"resource_id"`
	RequestNo         string  `json:"request_no,omitempty"`
	OperationID       *string `json:"operation_id"`
	PermissionVersion string  `json:"permission_version,omitempty"`
	Changed           bool    `json:"changed"`
}
type Audit struct {
	Target                int64
	Operation             string
	Application           *int64
	RequestNo             *string
	Before, After, Remark string
}
type Write struct {
	Actor                     Actor
	Key, Operation, RequestID string
	Admin                     bool
}
type Page struct {
	Page  int               `json:"page"`
	Size  int               `json:"page_size"`
	Total int64             `json:"total"`
	Items []json.RawMessage `json:"items"`
}
type Filter struct {
	Page, Size                                                              int
	Keyword, Status, UserID, Enabled, Account, ActorID, TargetID, Operation string
	Topups                                                                  []int64
}

func ID(raw string) (int64, error) {
	v, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || v <= 0 || strconv.FormatInt(v, 10) != raw {
		return 0, fault.New(400, "INVALID_ARGUMENT", "ID 必须为正整数字符串")
	}
	return v, nil
}
func Text(s string, max int, required bool) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max || strings.ContainsRune(s, 0) || (required && s == "") {
		return "", fault.New(400, "INVALID_ARGUMENT", "字段为空或超过长度限制")
	}
	return s, nil
}
func Like(s string) string {
	return "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(s) + "%"
}
func DataError() error {
	return fault.New(422, "DATA_INCOMPATIBLE", "历史数据无法安全处理，请联系管理员")
}
func Conflict(code string) error { return fault.New(409, code, "数据已变化，请刷新核对") }
func NotFound() error            { return fault.New(404, "NOT_FOUND", "记录不存在") }
func databaseError(e error) error {
	var pg *pgconn.PgError
	if errors.As(e, &pg) {
		switch pg.Code {
		case "23505":
			return Conflict("ORDER_UNAVAILABLE")
		case "40001", "40P01", "55P03", "57014":
			return fault.New(503, "WRITE_TEMPORARY_FAILURE", "写入暂不可用，请使用原请求重试")
		}
	}
	return e
}

func (s *Service) Target(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) error {
	var status int
	var deleted *time.Time
	e := q.QueryRow(ctx, "SELECT status,deleted_at FROM users WHERE id=$1", id).Scan(&status, &deleted)
	if errors.Is(e, pgx.ErrNoRows) {
		return NotFound()
	}
	if e != nil {
		return e
	}
	if status != 1 || deleted != nil {
		return Conflict("TARGET_UNAVAILABLE")
	}
	return nil
}
func (s *Service) Permission(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64, lock bool) error {
	sql := "SELECT enabled FROM invoice_user_permissions WHERE user_id=$1"
	if lock {
		sql += " FOR UPDATE"
	}
	var enabled bool
	e := q.QueryRow(ctx, sql, id).Scan(&enabled)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if !enabled {
		return fault.New(403, "INVOICE_NOT_ENABLED", "自助开票尚未开通，请联系管理员")
	}
	return nil
}
func (s *Service) Mutate(ctx context.Context, w Write, input any, fn func(pgx.Tx) (Result, *Audit, error)) (Result, error) {
	var result Result
	if s.Config.ReadOnly {
		return result, fault.New(503, "WRITE_TEMPORARY_FAILURE", "维护期间暂不接受变更")
	}
	if _, e := uuid.Parse(w.Key); e != nil {
		return result, fault.New(400, "INVALID_ARGUMENT", "缺少有效 Idempotency-Key")
	}
	payload, e := json.Marshal(input)
	if e != nil {
		return result, e
	}
	sum := sha256.Sum256(append([]byte(w.Operation+"\x00"), payload...))
	hash := hex.EncodeToString(sum[:])
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return result, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SET LOCAL lock_timeout='4s'; SET LOCAL statement_timeout='8s'"); e != nil {
		return result, e
	}
	writeID := uuid.NewString()
	now := time.Now().Unix()
	tag, e := tx.Exec(ctx, `INSERT INTO invoice_write_requests(id,actor_user_id,idempotency_key,operation,request_hash,response_ref,result_code,requires_admin,created_at) VALUES($1,$2,$3,$4,$5,'{}','OK',$6,$7) ON CONFLICT(actor_user_id,idempotency_key) DO NOTHING`, writeID, w.Actor.ID, w.Key, w.Operation, hash, w.Admin, now)
	if e != nil {
		return result, databaseError(e)
	}
	if tag.RowsAffected() == 0 {
		var oldHash string
		var raw []byte
		var admin bool
		e = tx.QueryRow(ctx, `SELECT request_hash,response_ref,requires_admin FROM invoice_write_requests WHERE actor_user_id=$1 AND idempotency_key=$2`, w.Actor.ID, w.Key).Scan(&oldHash, &raw, &admin)
		if e != nil {
			return result, e
		}
		if oldHash != hash {
			return result, Conflict("IDEMPOTENCY_CONFLICT")
		}
		if admin && !w.Actor.Admin() {
			return result, fault.New(403, "ADMIN_REQUIRED", "仅管理员可访问")
		}
		e = json.Unmarshal(raw, &result)
		return result, e
	}
	result, audit, e := fn(tx)
	if e != nil {
		return result, databaseError(e)
	}
	if audit != nil {
		kind := "user"
		if w.Admin {
			kind = "admin"
		}
		name := w.Actor.Name
		if name == "" {
			name = w.Actor.Username
		}
		var id int64
		e = tx.QueryRow(ctx, `INSERT INTO invoice_operation_logs(occurred_at,actor_type,actor_user_id,actor_name,target_user_id,operation_type,application_id,request_no,before_state,after_state,remark,request_id,write_request_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`, now, kind, w.Actor.ID, name, audit.Target, audit.Operation, audit.Application, audit.RequestNo, audit.Before, audit.After, audit.Remark, w.RequestID, writeID).Scan(&id)
		if e != nil {
			return result, e
		}
		sid := strconv.FormatInt(id, 10)
		result.OperationID = &sid
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return result, e
	}
	if _, e = tx.Exec(ctx, "UPDATE invoice_write_requests SET response_ref=$1 WHERE id=$2", raw, writeID); e != nil {
		return result, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Result{}, fault.New(503, "WRITE_TEMPORARY_FAILURE", "提交结果待确认，请使用原请求查询或重试")
	}
	return result, nil
}

func (s *Service) WriteResult(ctx context.Context, a Actor, key string) (any, error) {
	if _, e := uuid.Parse(key); e != nil {
		return nil, fault.New(400, "INVALID_ARGUMENT", "无效的请求键")
	}
	var raw json.RawMessage
	var admin bool
	e := s.DB.QueryRow(ctx, `SELECT response_ref,requires_admin FROM invoice_write_requests WHERE actor_user_id=$1 AND idempotency_key=$2`, a.ID, key).Scan(&raw, &admin)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, NotFound()
	}
	if e != nil {
		return nil, e
	}
	if admin && !a.Admin() {
		return nil, fault.New(403, "ADMIN_REQUIRED", "仅管理员可访问")
	}
	return map[string]any{"committed": true, "result": raw}, nil
}
func (s *Service) Me(ctx context.Context, a Actor) (any, error) {
	var enabled bool
	var version string
	var changed *int64
	e := s.DB.QueryRow(ctx, `SELECT coalesce(p.enabled,false),coalesce(p.version,0)::text,CASE WHEN p.source='default_denied' AND p.version=0 THEN NULL ELSE p.updated_at END FROM (SELECT $1::bigint AS id) u LEFT JOIN invoice_user_permissions p ON p.user_id=u.id`, a.ID).Scan(&enabled, &version, &changed)
	if e != nil {
		return nil, e
	}
	return map[string]any{"user": map[string]any{"id": strconv.FormatInt(a.ID, 10), "username": a.Username, "display_name": a.Name, "role": a.Role, "status": a.Status}, "invoice_permission": map[string]any{"enabled": enabled, "version": version, "changed_at": changed}, "capabilities": map[string]bool{"can_submit": enabled, "can_manage_titles": enabled, "can_read_history": true, "can_admin": a.Admin()}}, nil
}

func (s *Service) List(ctx context.Context, f Filter, selectSQL, from, where, order string, args ...any) (Page, error) {
	p := Page{Page: f.Page, Size: f.Size, Items: []json.RawMessage{}}
	if e := s.DB.QueryRow(ctx, "SELECT count(*) "+from+" WHERE "+where, args...).Scan(&p.Total); e != nil {
		return p, e
	}
	args = append(args, f.Size, int64(f.Page-1)*int64(f.Size))
	rows, e := s.DB.Query(ctx, "SELECT "+selectSQL+" "+from+" WHERE "+where+" ORDER BY "+order+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		var b json.RawMessage
		if e = rows.Scan(&b); e != nil {
			return p, e
		}
		p.Items = append(p.Items, b)
	}
	return p, rows.Err()
}
