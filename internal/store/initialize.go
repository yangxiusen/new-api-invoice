package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type InitStats struct {
	Users       int64  `json:"users"`
	Inserted    int64  `json:"inserted"`
	Enabled     int64  `json:"enabled"`
	Skipped     int64  `json:"skipped"`
	AlreadyDone bool   `json:"already_done"`
	Global      bool   `json:"global_enabled"`
	Source      string `json:"source"`
}

func Initialize(ctx context.Context, db *pgxpool.Pool, override *bool, dry bool) (InitStats, error) {
	var s InitStats
	tx, e := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return s, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(73190502)"); e != nil {
		return s, e
	}
	var done bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM invoice_initializations WHERE name='legacy-permissions-v1')").Scan(&done); e != nil {
		return s, e
	}
	if done {
		s.AlreadyDone = true
		return s, nil
	}
	var raw string
	s.Source = "options"
	if override != nil {
		s.Global = *override
		s.Source = "explicit_override"
		if s.Global {
			raw = "true"
		} else {
			raw = "false"
		}
	} else {
		if e = tx.QueryRow(ctx, `SELECT value FROM options WHERE key='SidebarModulesAdmin'`).Scan(&raw); e != nil {
			return s, errors.New("legacy global option missing; confirmed override required")
		}
		var v struct {
			Personal struct {
				Invoice *bool `json:"invoice"`
			} `json:"personal"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil || v.Personal.Invoice == nil {
			return s, errors.New("legacy global option invalid; confirmed override required")
		}
		s.Global = *v.Personal.Invoice
	}
	rows, e := tx.Query(ctx, `SELECT u.id,coalesce(u.invoice_enabled,false),EXISTS(SELECT 1 FROM invoice_user_permissions p WHERE p.user_id=u.id) FROM users u ORDER BY u.id`)
	if e != nil {
		return s, e
	}
	type item struct {
		id              int64
		enabled, exists bool
	}
	items := []item{}
	var maxID int64
	for rows.Next() {
		var x item
		if e = rows.Scan(&x.id, &x.enabled, &x.exists); e != nil {
			rows.Close()
			return s, e
		}
		items = append(items, x)
		if x.id > maxID {
			maxID = x.id
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return s, e
	}
	batch := uuid.NewString()
	now := time.Now().Unix()
	fingerprint := sha256.Sum256([]byte(raw))
	for _, x := range items {
		s.Users++
		if x.exists {
			s.Skipped++
			continue
		}
		enabled := s.Global || x.enabled
		s.Inserted++
		if enabled {
			s.Enabled++
		}
		if dry {
			continue
		}
		tag, e := tx.Exec(ctx, `INSERT INTO invoice_user_permissions(user_id,enabled,version,source,initialization_batch,created_at,updated_at) VALUES($1,$2,0,'legacy_import',$3,$4,$4) ON CONFLICT(user_id) DO NOTHING`, x.id, enabled, batch, now)
		if e != nil {
			return s, e
		}
		if tag.RowsAffected() == 0 {
			s.Inserted--
			s.Skipped++
			if enabled {
				s.Enabled--
			}
			continue
		}
		if _, e = tx.Exec(ctx, `INSERT INTO invoice_operation_logs(occurred_at,actor_type,actor_name,target_user_id,operation_type,after_state,request_id,initialization_batch) VALUES($1,'system_import','legacy-permissions-v1',$2,'permission_import',$3,$4,$5)`, now, x.id, map[bool]string{true: "enabled", false: "disabled"}[enabled], uuid.NewString(), batch); e != nil {
			return s, e
		}
	}
	if dry {
		return s, nil
	}
	if _, e = tx.Exec(ctx, `INSERT INTO invoice_initializations VALUES('legacy-permissions-v1',$1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, batch, s.Global, s.Source, hex.EncodeToString(fingerprint[:]), maxID, s.Users, s.Inserted, s.Enabled, s.Skipped, now); e != nil {
		return s, e
	}
	return s, tx.Commit(ctx)
}
