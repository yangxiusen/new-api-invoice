package billing

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"invoice/internal/fault"
	"invoice/internal/money"
)

type Order struct {
	ID        string `json:"id"`
	Trade     string `json:"trade_no"`
	Method    string `json:"payment_method"`
	Provider  string `json:"payment_provider"`
	Money     string `json:"pay_money"`
	Cents     string `json:"amount_cents"`
	Currency  string `json:"currency"`
	Created   *int64 `json:"create_time"`
	Completed *int64 `json:"complete_time"`
	Paid      *int64 `json:"payment_time"`
	Source    string `json:"payment_time_source"`
	Quota     *int64 `json:"-"`
}

const orderSelect = `t.id::text,coalesce(t.trade_no,''),coalesce(t.payment_method,''),coalesce(t.payment_provider,''),t.money::text,t.create_time,t.complete_time,t.amount`

func (s *Service) scanOrder(row interface{ Scan(...any) error }) (Order, error) {
	var o Order
	var raw *string
	e := row.Scan(&o.ID, &o.Trade, &o.Method, &o.Provider, &raw, &o.Created, &o.Completed, &o.Quota)
	if e != nil {
		return o, e
	}
	if raw == nil {
		return o, DataError()
	}
	o.Money = *raw
	cents, e := money.Cents(o.Money)
	if e != nil {
		return o, DataError()
	}
	o.Cents = strconv.FormatInt(cents, 10)
	if s.Config.Currency(o.Provider, o.Method) != "CNY" {
		return o, fault.New(422, "CURRENCY_UNSUPPORTED", "订单币种未经确认或不是人民币")
	}
	o.Currency = "CNY"
	o.Source = "complete"
	o.Paid = o.Completed
	if o.Completed == nil || *o.Completed <= 0 {
		o.Source = "create_fallback"
		o.Paid = o.Created
	}
	return o, nil
}
func (s *Service) Orders(ctx context.Context, user int64, f Filter, admin bool) (Page, error) {
	p := Page{Page: f.Page, Size: f.Size, Items: []json.RawMessage{}}
	if admin {
		if e := s.Target(ctx, s.DB, user); e != nil {
			return p, e
		}
	} else {
		if e := s.Permission(ctx, s.DB, user, false); e != nil {
			return p, e
		}
	}
	const where = `FROM top_ups t WHERE t.user_id=$1 AND t.status='success' AND t.money>0 AND coalesce(t.trade_no,'') ILIKE $2 AND NOT EXISTS(SELECT 1 FROM invoice_application_items i WHERE i.top_up_id=t.id AND i.active=true) AND (coalesce(cardinality($3::bigint[]),0)=0 OR t.id=ANY($3))`
	if e := s.DB.QueryRow(ctx, "SELECT count(*) "+where, user, Like(f.Keyword), f.Topups).Scan(&p.Total); e != nil {
		return p, e
	}
	rows, e := s.DB.Query(ctx, "SELECT "+orderSelect+" "+where+` ORDER BY CASE WHEN t.complete_time>0 THEN t.complete_time ELSE t.create_time END DESC NULLS LAST,t.id DESC LIMIT $4 OFFSET $5`, user, Like(f.Keyword), f.Topups, f.Size, int64(f.Page-1)*int64(f.Size))
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		o, e := s.scanOrder(rows)
		if e != nil {
			return p, e
		}
		raw, e := json.Marshal(o)
		if e != nil {
			return p, e
		}
		p.Items = append(p.Items, raw)
	}
	return p, rows.Err()
}

type CreateInput struct {
	UserID   string            `json:"user_id,omitempty"`
	TitleID  string            `json:"title_id"`
	Topups   []string          `json:"topup_ids"`
	Remark   string            `json:"user_remark"`
	Expected map[string]string `json:"expected_order_amounts,omitempty"`
}

func (s *Service) Create(ctx context.Context, w Write, in CreateInput) (Result, error) {
	user := w.Actor.ID
	var e error
	if w.Admin {
		user, e = ID(in.UserID)
		if e != nil {
			return Result{}, e
		}
	} else if in.UserID != "" {
		return Result{}, fault.New(400, "INVALID_ARGUMENT", "不接受 user_id")
	}
	title, e := ID(in.TitleID)
	if e != nil {
		return Result{}, e
	}
	if len(in.Topups) < 1 || len(in.Topups) > 100 {
		return Result{}, fault.New(400, "INVALID_ARGUMENT", "请选择 1 至 100 笔订单")
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, raw := range in.Topups {
		id, e := ID(raw)
		if e != nil {
			return Result{}, e
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	in.Topups = []string{}
	for _, id := range ids {
		in.Topups = append(in.Topups, strconv.FormatInt(id, 10))
	}
	in.Remark, e = Text(in.Remark, 512, false)
	if e != nil {
		return Result{}, e
	}
	return s.Mutate(ctx, w, in, func(tx pgx.Tx) (Result, *Audit, error) {
		var r Result
		if !w.Admin {
			if e := s.Permission(ctx, tx, user, true); e != nil {
				return r, nil, e
			}
		}
		if e := s.Target(ctx, tx, user); e != nil {
			return r, nil, e
		}
		var t TitleInput
		var typ string
		e := tx.QueryRow(ctx, `SELECT coalesce(title_type,''),coalesce(company_name,''),coalesce(tax_no,''),coalesce(email,''),coalesce(registered_address,''),coalesce(registered_phone,''),coalesce(bank_name,''),coalesce(bank_account,'') FROM invoice_titles WHERE id=$1 AND user_id=$2 FOR UPDATE`, title, user).Scan(&typ, &t.Company, &t.Tax, &t.Email, &t.Address, &t.Phone, &t.Bank, &t.Account)
		if errors.Is(e, pgx.ErrNoRows) {
			return r, nil, NotFound()
		}
		if e != nil {
			return r, nil, e
		}
		if typ != "enterprise" || t.Validate() != nil {
			return r, nil, DataError()
		}
		rows, e := tx.Query(ctx, "SELECT "+orderSelect+` FROM top_ups t WHERE t.id=ANY($1) AND t.user_id=$2 AND t.status='success' AND t.money>0 ORDER BY t.id`, ids, user)
		if e != nil {
			return r, nil, e
		}
		orders := []Order{}
		total := int64(0)
		for rows.Next() {
			o, e := s.scanOrder(rows)
			if e != nil {
				rows.Close()
				return r, nil, e
			}
			v, _ := strconv.ParseInt(o.Cents, 10, 64)
			total, e = money.Add(total, v)
			if e != nil {
				rows.Close()
				return r, nil, DataError()
			}
			orders = append(orders, o)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return r, nil, e
		}
		if len(orders) != len(ids) {
			return r, nil, Conflict("ORDER_UNAVAILABLE")
		}
		if in.Expected != nil {
			if len(in.Expected) != len(orders) {
				return r, nil, fault.New(400, "INVALID_ARGUMENT", "订单确认金额不完整")
			}
			for _, o := range orders {
				if in.Expected[o.ID] != o.Cents {
					return r, nil, Conflict("ORDER_UNAVAILABLE")
				}
			}
		}
		var occupied bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM invoice_application_items WHERE top_up_id=ANY($1) AND active=true)`, ids).Scan(&occupied); e != nil {
			return r, nil, e
		}
		if occupied {
			return r, nil, Conflict("ORDER_UNAVAILABLE")
		}
		now := time.Now().Unix()
		number := "INV-" + time.Now().UTC().Format("20060102") + "-" + uuid.NewString()
		var appID int64
		e = tx.QueryRow(ctx, `INSERT INTO invoice_applications(request_no,user_id,status,total_amount_cents,currency,title_type,company_name,tax_no,email,registered_address,registered_phone,bank_name,bank_account,user_remark,admin_remark,created_at,updated_at) VALUES($1,$2,'pending',$3,'CNY','enterprise',$4,$5,$6,$7,$8,$9,$10,$11,'',$12,$12) RETURNING id`, number, user, total, t.Company, t.Tax, t.Email, t.Address, t.Phone, t.Bank, t.Account, in.Remark, now).Scan(&appID)
		if e != nil {
			return r, nil, e
		}
		for _, o := range orders {
			id, _ := ID(o.ID)
			cents, _ := strconv.ParseInt(o.Cents, 10, 64)
			_, e = tx.Exec(ctx, `INSERT INTO invoice_application_items(application_id,user_id,top_up_id,trade_no,payment_method,payment_provider,top_up_create_time,top_up_complete_time,top_up_amount,pay_money,amount_cents,active,active_key,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,'active',$12)`, appID, user, id, o.Trade, o.Method, o.Provider, o.Created, o.Completed, o.Quota, o.Money, cents, now)
			if e != nil {
				return r, nil, e
			}
		}
		// Re-read without locking the host table. This narrows the external refund
		// window and detects changes during preparation; it is not a cross-service lock.
		latest, e := tx.Query(ctx, "SELECT "+orderSelect+` FROM top_ups t WHERE t.id=ANY($1) AND t.user_id=$2 AND t.status='success' AND t.money>0 ORDER BY t.id`, ids, user)
		if e != nil {
			return r, nil, e
		}
		n := 0
		changed := false
		for latest.Next() {
			o, err := s.scanOrder(latest)
			if err != nil {
				latest.Close()
				return r, nil, err
			}
			if n >= len(orders) {
				changed = true
				break
			}
			before, _ := json.Marshal(orders[n])
			after, _ := json.Marshal(o)
			if string(before) != string(after) {
				changed = true
			}
			n++
		}
		latest.Close()
		if e = latest.Err(); e != nil {
			return r, nil, e
		}
		if changed || n != len(orders) {
			return r, nil, Conflict("ORDER_UNAVAILABLE")
		}
		op := "create_user"
		if w.Admin {
			op = "create_admin"
		}
		r = Result{ResourceType: "application", ResourceID: strconv.FormatInt(appID, 10), RequestNo: number, Changed: true}
		return r, &Audit{Target: user, Operation: op, Application: &appID, RequestNo: &number, After: "pending"}, nil
	})
}

const summaryJSON = `jsonb_build_object('id',a.id::text,'request_no',a.request_no,'user_id',a.user_id::text,'status',a.status,'total_amount_cents',a.total_amount_cents::text,'currency',a.currency,'company_name',coalesce(a.company_name,''),'created_at',a.created_at,'invoiced_at',CASE WHEN a.status='invoiced' THEN a.invoiced_at ELSE NULL END,'item_count',(SELECT count(*) FROM invoice_application_items i WHERE i.application_id=a.id))`

func (s *Service) Applications(ctx context.Context, a Actor, f Filter, admin bool) (Page, error) {
	user := ""
	selectSQL := summaryJSON
	from := "FROM invoice_applications a"
	if !admin {
		user = strconv.FormatInt(a.ID, 10)
	} else {
		user = f.UserID
		from += " LEFT JOIN users u ON u.id=a.user_id"
		selectSQL += ` || jsonb_build_object('username',u.username,'display_name',u.display_name)`
	}
	return s.List(ctx, f, selectSQL, from, `($1='' OR a.user_id::text=$1) AND ($2='' OR a.status=$2) AND (coalesce(a.request_no,'') ILIKE $3 OR coalesce(a.company_name,'') ILIKE $3 OR ($4 AND EXISTS(SELECT 1 FROM invoice_application_items i WHERE i.application_id=a.id AND coalesce(i.trade_no,'') ILIKE $3)))`, "a.created_at DESC NULLS LAST,a.id DESC", user, f.Status, Like(f.Keyword), admin)
}
func (s *Service) Detail(ctx context.Context, a Actor, id int64, admin bool) (json.RawMessage, error) {
	extra := `jsonb_build_object('title_type',coalesce(a.title_type,''),'tax_no',coalesce(a.tax_no,''),'email',coalesce(a.email,''),'registered_address',coalesce(a.registered_address,''),'registered_phone',coalesce(a.registered_phone,''),'bank_name',coalesce(a.bank_name,''),'bank_account',coalesce(a.bank_account,''),'user_remark',coalesce(a.user_remark,''),'admin_remark',coalesce(a.admin_remark,''),'items',coalesce((SELECT jsonb_agg(jsonb_build_object('id',i.id::text,'topup_id',i.top_up_id::text,'trade_no',coalesce(i.trade_no,''),'payment_method',coalesce(i.payment_method,''),'payment_provider',coalesce(i.payment_provider,''),'topup_create_time',i.top_up_create_time,'topup_complete_time',i.top_up_complete_time,'payment_time_source',CASE WHEN i.top_up_complete_time>0 THEN 'complete' ELSE 'create_fallback' END,'pay_money',coalesce(i.pay_money,''),'amount_cents',i.amount_cents::text) ORDER BY i.id) FROM invoice_application_items i WHERE i.application_id=a.id),'[]'::jsonb),'file',CASE WHEN a.status='invoiced' THEN jsonb_build_object('name',coalesce(a.invoice_file_name,''),'mime',coalesce(a.invoice_file_mime,''),'size',a.invoice_file_size::text,'downloadable',coalesce(a.invoice_file_path,'')<>'') ELSE NULL END)`
	if admin {
		extra += ` || jsonb_build_object('creation_source',coalesce((SELECT CASE WHEN l.operation_type='create_admin' THEN 'admin' ELSE 'user' END FROM invoice_operation_logs l WHERE l.application_id=a.id AND l.operation_type IN ('create_user','create_admin') ORDER BY l.id LIMIT 1),'legacy'),'operator',(SELECT jsonb_build_object('id',l.actor_user_id::text,'name',l.actor_name) FROM invoice_operation_logs l WHERE l.application_id=a.id AND l.operation_type IN ('create_user','create_admin') ORDER BY l.id LIMIT 1))`
	}
	var raw json.RawMessage
	e := s.DB.QueryRow(ctx, "SELECT "+summaryJSON+" || "+extra+" FROM invoice_applications a WHERE a.id=$1 AND ($2 OR a.user_id=$3)", id, admin, a.ID).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, NotFound()
	}
	return raw, e
}
func (s *Service) Transition(ctx context.Context, w Write, id int64, remark string) (Result, error) {
	var e error
	remark, e = Text(remark, 512, w.Admin)
	if e != nil {
		return Result{}, e
	}
	return s.Mutate(ctx, w, map[string]string{"admin_remark": remark}, func(tx pgx.Tx) (Result, *Audit, error) {
		var r Result
		var user int64
		var status, number string
		e := tx.QueryRow(ctx, "SELECT user_id,status,request_no FROM invoice_applications WHERE id=$1 AND ($2 OR user_id=$3) FOR UPDATE", id, w.Admin, w.Actor.ID).Scan(&user, &status, &number)
		if errors.Is(e, pgx.ErrNoRows) {
			return r, nil, NotFound()
		}
		if e != nil {
			return r, nil, e
		}
		if status != "pending" {
			return r, nil, Conflict("STATE_CONFLICT")
		}
		next, op := "cancelled", "cancel_user"
		if w.Admin {
			next, op = "rejected", "reject_admin"
		}
		if _, e = tx.Exec(ctx, `UPDATE invoice_applications SET status=$1,admin_remark=CASE WHEN $2 THEN $3 ELSE admin_remark END,updated_at=$4 WHERE id=$5 AND status='pending'`, next, w.Admin, remark, time.Now().Unix(), id); e != nil {
			return r, nil, e
		}
		if _, e = tx.Exec(ctx, `UPDATE invoice_application_items SET active=false,active_key='released:'||id::text||':'||$1 WHERE application_id=$2 AND active=true`, uuid.NewString()[:12], id); e != nil {
			return r, nil, e
		}
		r = Result{ResourceType: "application", ResourceID: strconv.FormatInt(id, 10), RequestNo: number, Changed: true}
		return r, &Audit{Target: user, Operation: op, Application: &id, RequestNo: &number, Before: "pending", After: next, Remark: remark}, nil
	})
}
