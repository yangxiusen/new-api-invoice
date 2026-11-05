package billing

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"invoice/internal/fault"
)

const usersJSON = `json_build_object('id',u.id::text,'username',coalesce(u.username,''),'display_name',coalesce(u.display_name,''),'email',coalesce(u.email,''),'account_status',CASE WHEN u.status=1 THEN 'enabled' ELSE 'disabled' END,'invoice_enabled',coalesce(p.enabled,false),'permission_version',coalesce(p.version,0)::text,'permission_changed_at',CASE WHEN p.source='default_denied' AND p.version=0 THEN NULL ELSE p.updated_at END,'permission_source',p.source,'can_change_permission',u.status=1,'can_create_application',u.status=1)`

func (s *Service) Users(ctx context.Context, f Filter) (Page, error) {
	return s.List(ctx, f, usersJSON, "FROM users u LEFT JOIN invoice_user_permissions p ON p.user_id=u.id", `u.deleted_at IS NULL AND (coalesce(u.username,'') ILIKE $1 OR coalesce(u.display_name,'') ILIKE $1 OR coalesce(u.email,'') ILIKE $1) AND ($2='' OR u.id::text=$2) AND ($3='' OR coalesce(p.enabled,false)::text=$3) AND ($4='' OR (CASE WHEN u.status=1 THEN 'enabled' ELSE 'disabled' END)=$4)`, "u.id DESC", Like(f.Keyword), f.UserID, f.Enabled, f.Account)
}

type PermissionInput struct {
	Enabled         *bool  `json:"enabled"`
	ExpectedVersion string `json:"expected_version"`
	Remark          string `json:"remark"`
}

func (s *Service) ChangePermission(ctx context.Context, w Write, user int64, in PermissionInput) (Result, error) {
	version, e := strconv.ParseInt(in.ExpectedVersion, 10, 64)
	if e != nil || version < 0 || strconv.FormatInt(version, 10) != in.ExpectedVersion || in.Enabled == nil {
		return Result{}, fault.New(400, "INVALID_ARGUMENT", "enabled 和 expected_version 必填")
	}
	in.Remark, e = Text(in.Remark, 512, false)
	if e != nil {
		return Result{}, e
	}
	return s.Mutate(ctx, w, in, func(tx pgx.Tx) (Result, *Audit, error) {
		r := Result{ResourceType: "permission", ResourceID: strconv.FormatInt(user, 10)}
		if e := s.Target(ctx, tx, user); e != nil {
			return r, nil, e
		}
		now := time.Now().Unix()
		if _, e := tx.Exec(ctx, `INSERT INTO invoice_user_permissions(user_id,enabled,version,source,created_at,updated_at) VALUES($1,false,0,'default_denied',$2,$2) ON CONFLICT DO NOTHING`, user, now); e != nil {
			return r, nil, e
		}
		var enabled bool
		var old int64
		if e := tx.QueryRow(ctx, "SELECT enabled,version FROM invoice_user_permissions WHERE user_id=$1 FOR UPDATE", user).Scan(&enabled, &old); e != nil {
			return r, nil, e
		}
		if old != version {
			return r, nil, Conflict("VERSION_CONFLICT")
		}
		r.PermissionVersion = strconv.FormatInt(old, 10)
		if enabled == *in.Enabled {
			return r, nil, nil
		}
		e := error(nil)
		_, e = tx.Exec(ctx, `UPDATE invoice_user_permissions SET enabled=$1,version=version+1,source='manual',updated_by=$2,remark=$3,updated_at=$4 WHERE user_id=$5`, *in.Enabled, w.Actor.ID, in.Remark, now, user)
		if e != nil {
			return r, nil, e
		}
		r.Changed = true
		r.PermissionVersion = strconv.FormatInt(old+1, 10)
		op := "permission_revoke"
		if *in.Enabled {
			op = "permission_enable"
		}
		return r, &Audit{Target: user, Operation: op, Before: strconv.FormatBool(enabled), After: strconv.FormatBool(*in.Enabled), Remark: in.Remark}, nil
	})
}
