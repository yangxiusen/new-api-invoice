package billing

import (
	"context"
	"errors"
	"net/mail"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"invoice/internal/fault"
)

type TitleInput struct {
	Company string `json:"company_name"`
	Tax     string `json:"tax_no"`
	Email   string `json:"email"`
	Address string `json:"registered_address"`
	Phone   string `json:"registered_phone"`
	Bank    string `json:"bank_name"`
	Account string `json:"bank_account"`
}

func (t *TitleInput) Validate() error {
	for _, f := range []struct {
		v   *string
		max int
		req bool
	}{{&t.Company, 255, true}, {&t.Tax, 64, true}, {&t.Email, 255, true}, {&t.Address, 512, false}, {&t.Phone, 64, false}, {&t.Bank, 255, false}, {&t.Account, 128, false}} {
		s, e := Text(*f.v, f.max, f.req)
		if e != nil {
			return e
		}
		*f.v = s
	}
	a, e := mail.ParseAddress(t.Email)
	if e != nil || a.Address != t.Email {
		return fault.New(400, "INVALID_ARGUMENT", "接收邮箱格式不正确")
	}
	return nil
}

const titleJSON = `json_build_object('id',t.id::text,'user_id',t.user_id::text,'title_type',coalesce(t.title_type,''),'company_name',coalesce(t.company_name,''),'tax_no',coalesce(t.tax_no,''),'email',coalesce(t.email,''),'registered_address',coalesce(t.registered_address,''),'registered_phone',coalesce(t.registered_phone,''),'bank_name',coalesce(t.bank_name,''),'bank_account',coalesce(t.bank_account,''),'created_at',t.created_at,'updated_at',t.updated_at)`

func (s *Service) Titles(ctx context.Context, user int64, f Filter, admin bool) (Page, error) {
	if admin {
		if e := s.Target(ctx, s.DB, user); e != nil {
			return Page{}, e
		}
	} else {
		if e := s.Permission(ctx, s.DB, user, false); e != nil {
			return Page{}, e
		}
	}
	return s.List(ctx, f, titleJSON, "FROM invoice_titles t", `t.user_id=$1 AND (coalesce(t.company_name,'') ILIKE $2 OR coalesce(t.tax_no,'') ILIKE $2)`, "t.updated_at DESC NULLS LAST,t.id DESC", user, Like(f.Keyword))
}
func (s *Service) SaveTitle(ctx context.Context, w Write, user, id int64, t TitleInput, remove bool) (Result, error) {
	if !remove {
		if e := t.Validate(); e != nil {
			return Result{}, e
		}
	}
	return s.Mutate(ctx, w, t, func(tx pgx.Tx) (Result, *Audit, error) {
		var r Result
		if w.Admin {
			if e := s.Target(ctx, tx, user); e != nil {
				return r, nil, e
			}
		} else {
			if e := s.Permission(ctx, tx, user, true); e != nil {
				return r, nil, e
			}
		}
		op := "title_create"
		now := time.Now().Unix()
		if id != 0 {
			var found int64
			e := tx.QueryRow(ctx, "SELECT id FROM invoice_titles WHERE id=$1 AND user_id=$2 FOR UPDATE", id, user).Scan(&found)
			if errors.Is(e, pgx.ErrNoRows) {
				return r, nil, NotFound()
			}
			if e != nil {
				return r, nil, e
			}
		}
		var e error
		switch {
		case remove:
			op = "title_delete"
			_, e = tx.Exec(ctx, "DELETE FROM invoice_titles WHERE id=$1 AND user_id=$2", id, user)
		case id != 0:
			op = "title_update"
			_, e = tx.Exec(ctx, `UPDATE invoice_titles SET company_name=$1,tax_no=$2,email=$3,registered_address=$4,registered_phone=$5,bank_name=$6,bank_account=$7,updated_at=$8 WHERE id=$9 AND user_id=$10`, t.Company, t.Tax, t.Email, t.Address, t.Phone, t.Bank, t.Account, now, id, user)
		default:
			if w.Admin {
				op = "title_create_admin"
			}
			e = tx.QueryRow(ctx, `INSERT INTO invoice_titles(user_id,title_type,company_name,tax_no,email,registered_address,registered_phone,bank_name,bank_account,created_at,updated_at) VALUES($1,'enterprise',$2,$3,$4,$5,$6,$7,$8,$9,$9) RETURNING id`, user, t.Company, t.Tax, t.Email, t.Address, t.Phone, t.Bank, t.Account, now).Scan(&id)
		}
		if e != nil {
			return r, nil, e
		}
		r = Result{ResourceType: "title", ResourceID: strconv.FormatInt(id, 10), Changed: true}
		return r, &Audit{Target: user, Operation: op, After: r.ResourceID}, nil
	})
}
