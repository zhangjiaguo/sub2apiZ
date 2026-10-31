package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ReadGroupUsageReport 使用只读可重复读事务，所有聚合绑定到同一分组、时间范围与用户集合。
// 成员包括期间实际使用者和明确关联该分组的现有用户，不将公开分组扩展为全站用户。
func (r *UserMessageViewerRepository) ReadGroupUsageReport(ctx context.Context, groupID int64, from, to time.Time) (*service.GroupUsageReport, error) {
	if groupID <= 0 {
		return nil, service.ErrUsageReportGroupRequired
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	report := &service.GroupUsageReport{GroupID: groupID, StartTime: from, EndTime: to}
	err = tx.QueryRowContext(ctx, `SELECT coalesce(name,'') FROM groups WHERE id=$1 AND deleted_at IS NULL`, groupID).Scan(&report.GroupName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUsageReportGroupNotFound
	}
	if err != nil {
		return nil, err
	}
	args := []any{groupID, from, to}
	metrics := `count(*) AS requests, coalesce(sum(l.input_tokens),0) AS input_tokens,
		coalesce(sum(l.output_tokens),0) AS output_tokens, coalesce(sum(l.cache_read_tokens),0) AS cache_read_tokens,
		coalesce(sum(l.cache_creation_tokens),0) AS cache_write_tokens,
		coalesce(sum(l.actual_cost),0)::float8 AS actual_cost, coalesce(sum(l.total_cost),0)::float8 AS standard_cost`
	where := `l.group_id=$1 AND l.created_at >= $2 AND l.created_at < $3`
	query := `WITH usage AS (SELECT l.user_id, ` + metrics + ` FROM usage_logs l WHERE ` + where + ` GROUP BY l.user_id)
	SELECT u.id, coalesce(u.username,''), coalesce(u.email,''), coalesce(usage.requests,0),
		coalesce(usage.input_tokens,0), coalesce(usage.output_tokens,0), coalesce(usage.cache_read_tokens,0),
		coalesce(usage.cache_write_tokens,0), coalesce(usage.actual_cost,0), coalesce(usage.standard_cost,0)
	FROM users u LEFT JOIN usage ON usage.user_id=u.id
	WHERE u.deleted_at IS NULL AND (usage.user_id IS NOT NULL
		OR EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id=u.id AND k.group_id=$1 AND k.deleted_at IS NULL)
		OR EXISTS (SELECT 1 FROM user_allowed_groups a WHERE a.user_id=u.id AND a.group_id=$1)
		OR EXISTS (SELECT 1 FROM user_subscriptions s WHERE s.user_id=u.id AND s.group_id=$1 AND s.deleted_at IS NULL
			AND s.status='active' AND s.starts_at <= $3 AND s.expires_at > $3))
	ORDER BY coalesce(usage.actual_cost,0) DESC, u.id ASC`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var user service.UsageReportUser
		if err = rows.Scan(&user.UserID, &user.Username, &user.UserEmail, &user.Requests, &user.InputTokens, &user.OutputTokens, &user.CacheRead, &user.CacheWrite, &user.ActualCost, &user.StandardCost); err != nil {
			rows.Close()
			return nil, err
		}
		report.Users = append(report.Users, user)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// 只从实际用量生成明细，缺失日期由展示层按明确统计区间补成零值。
	query = `SELECT l.user_id, to_char(l.created_at AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD'), ` + metrics + `
		FROM usage_logs l JOIN users u ON u.id=l.user_id AND u.deleted_at IS NULL WHERE ` + where + `
		GROUP BY l.user_id, (l.created_at AT TIME ZONE 'Asia/Shanghai')::date, to_char(l.created_at AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD')
		ORDER BY 2, l.user_id`
	rows, err = tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day service.UsageReportDay
		if err = rows.Scan(&day.UserID, &day.Date, &day.Requests, &day.InputTokens, &day.OutputTokens, &day.CacheRead, &day.CacheWrite, &day.ActualCost, &day.StandardCost); err != nil {
			rows.Close()
			return nil, err
		}
		report.Daily = append(report.Daily, day)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	query = `SELECT l.user_id, coalesce(nullif(l.model,''),'未标明型号'), ` + metrics + `
		FROM usage_logs l JOIN users u ON u.id=l.user_id AND u.deleted_at IS NULL WHERE ` + where + `
		GROUP BY l.user_id, coalesce(nullif(l.model,''),'未标明型号') ORDER BY 2, l.user_id`
	rows, err = tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var model service.UsageReportModel
		if err = rows.Scan(&model.UserID, &model.Model, &model.Requests, &model.InputTokens, &model.OutputTokens, &model.CacheRead, &model.CacheWrite, &model.ActualCost, &model.StandardCost); err != nil {
			rows.Close()
			return nil, err
		}
		report.Models = append(report.Models, model)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return report, nil
}
