package billing

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"invoice/internal/files"
)

func (s *Service) Complete(ctx context.Context, w Write, id int64, p files.Prepared, remark string) (Result, error) {
	var e error
	remark, e = Text(remark, 512, false)
	if e != nil {
		return Result{}, e
	}
	in := struct {
		Name, Digest, MIME, Remark string
		Size                       int64
	}{p.Name, p.Digest, p.MIME, remark, p.Size}
	return s.Mutate(ctx, w, in, func(tx pgx.Tx) (Result, *Audit, error) {
		var r Result
		var user int64
		var status, number string
		e := tx.QueryRow(ctx, "SELECT user_id,status,request_no FROM invoice_applications WHERE id=$1 FOR UPDATE", id).Scan(&user, &status, &number)
		if errors.Is(e, pgx.ErrNoRows) {
			return r, nil, NotFound()
		}
		if e != nil {
			return r, nil, e
		}
		if status != "pending" {
			return r, nil, Conflict("STATE_CONFLICT")
		}
		now := time.Now().Unix()
		_, e = tx.Exec(ctx, `UPDATE invoice_applications SET status='invoiced',invoice_file_name=$1,invoice_file_path=$2,invoice_file_mime=$3,invoice_file_size=$4,admin_remark=$5,invoiced_at=$6,updated_at=$6 WHERE id=$7 AND status='pending'`, p.Name, p.Path, p.MIME, p.Size, remark, now, id)
		if e != nil {
			return r, nil, e
		}
		r = Result{ResourceType: "application", ResourceID: strconv.FormatInt(id, 10), RequestNo: number, Changed: true}
		return r, &Audit{Target: user, Operation: "complete_admin", Application: &id, RequestNo: &number, Before: "pending", After: "invoiced", Remark: remark}, nil
	})
}

type Download struct {
	Path, Name, MIME string
	Size             int64
}

func (s *Service) Download(ctx context.Context, a Actor, id int64, admin bool) (Download, error) {
	var d Download
	var state string
	e := s.DB.QueryRow(ctx, `SELECT status,coalesce(invoice_file_path,''),coalesce(invoice_file_name,''),coalesce(invoice_file_mime,''),coalesce(invoice_file_size,0) FROM invoice_applications WHERE id=$1 AND ($2 OR user_id=$3)`, id, admin, a.ID).Scan(&state, &d.Path, &d.Name, &d.MIME, &d.Size)
	if errors.Is(e, pgx.ErrNoRows) {
		return d, NotFound()
	}
	if e != nil {
		return d, e
	}
	if state != "invoiced" {
		return d, Conflict("STATE_CONFLICT")
	}
	return d, nil
}
