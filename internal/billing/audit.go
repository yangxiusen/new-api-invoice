package billing

import "context"

var Operations = map[string]bool{"permission_import": true, "permission_enable": true, "permission_revoke": true, "title_create": true, "title_update": true, "title_delete": true, "title_create_admin": true, "create_user": true, "create_admin": true, "cancel_user": true, "reject_admin": true, "complete_admin": true}

func (s *Service) Operations(ctx context.Context, f Filter) (Page, error) {
	return s.List(ctx, f, `jsonb_build_object('id',l.id::text,'occurred_at',l.occurred_at,'actor_type',l.actor_type,'actor_user_id',l.actor_user_id::text,'actor_name',l.actor_name,'target_user_id',l.target_user_id::text,'operation_type',l.operation_type,'application_id',l.application_id::text,'request_no',l.request_no,'before_state',l.before_state,'after_state',l.after_state,'remark',l.remark,'result','success','request_id',l.request_id::text)`, "FROM invoice_operation_logs l", `($1='' OR l.target_user_id::text=$1) AND ($2='' OR l.actor_user_id::text=$2) AND ($3='' OR l.operation_type=$3) AND (l.actor_name ILIKE $4 OR coalesce(l.request_no,'') ILIKE $4)`, "l.occurred_at DESC,l.id DESC", f.TargetID, f.ActorID, f.Operation, Like(f.Keyword))
}
