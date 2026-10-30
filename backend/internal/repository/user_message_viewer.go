// Package repository —— user_message_viewers 授权与分组用量排行查询。
package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type UserMessageViewerRepository struct {
	db *sql.DB
}

func NewUserMessageViewerRepository(db *sql.DB) *UserMessageViewerRepository {
	return &UserMessageViewerRepository{db: db}
}

// UserMessageViewer 别名（service.UserMessageViewerEntry）。
type UserMessageViewer = service.UserMessageViewerEntry

// GroupUsageRanking 别名（service.GroupUsageRanking）。
type GroupUsageRanking = service.GroupUsageRanking

// List 全量授权清单（管理页用）。
func (r *UserMessageViewerRepository) List(ctx context.Context) ([]*UserMessageViewer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT v.id, v.user_id, coalesce(u.username,''), coalesce(u.email,''),
			v.group_id, coalesce(g.name,''), v.created_at
		FROM user_message_viewers v
		JOIN users u ON u.id = v.user_id
		JOIN groups g ON g.id = v.group_id
		ORDER BY v.group_id, v.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*UserMessageViewer{}
	for rows.Next() {
		item := &UserMessageViewer{}
		if err := rows.Scan(&item.ID, &item.UserID, &item.Username, &item.UserEmail,
			&item.GroupID, &item.GroupName, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GroupsForUser 返回用户被授权可见的分组 ID 集。
func (r *UserMessageViewerRepository) GroupsForUser(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT group_id FROM user_message_viewers WHERE user_id=$1 ORDER BY group_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Grant 授予（幂等）。
func (r *UserMessageViewerRepository) Grant(ctx context.Context, userID, groupID, createdBy int64) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_message_viewers (user_id, group_id, created_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, group_id) DO NOTHING`, userID, groupID, createdBy)
	return err
}

// Revoke 收回单条授权。
func (r *UserMessageViewerRepository) Revoke(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM user_message_viewers WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return service.ErrUserMessageViewerNotFound
	}
	return nil
}

// RankGroupUsage 按费用稳定排序分页；管理员全分组列表也展示当前时段用量为零的用户。
// 指定分组时仅返回在该分组有用量的用户，查看者不会借此枚举其他分组用户。
func (r *UserMessageViewerRepository) RankGroupUsage(ctx context.Context, groupID int64, since time.Time, page, pageSize int) (*service.GroupUsageRankingList, error) {
	if pageSize <= 0 {
		pageSize = 20
	} else if pageSize > 100 {
		pageSize = 100
	}
	if page < 1 {
		page = 1
	}
	args := []any{since}
	usageWhere := "l.created_at >= $1"
	userWhere := "u.deleted_at IS NULL"
	countQuery := "SELECT count(*) FROM users u WHERE " + userWhere
	var countArgs []any
	if groupID > 0 {
		args = append(args, groupID)
		usageWhere += " AND l.group_id = $2"
		userWhere += " AND usage.user_id IS NOT NULL"
		countQuery += " AND EXISTS (SELECT 1 FROM usage_logs l WHERE l.user_id = u.id AND " + usageWhere + ")"
		countArgs = args
	}
	result := &service.GroupUsageRankingList{Items: []*GroupUsageRanking{}, Page: page, PageSize: pageSize, Pages: 1}
	if err := r.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&result.Total); err != nil {
		return nil, err
	}
	if result.Total == 0 {
		result.Page = 1
		return result, nil
	}
	result.Pages = int((result.Total-1)/int64(pageSize)) + 1
	if result.Page > result.Pages {
		result.Page = result.Pages
	}
	limitArg := len(args) + 1
	args = append(args, pageSize, (result.Page-1)*pageSize)
	query, _ := groupUsageRankingQuery(groupID, since)
	query += " LIMIT " + fmt.Sprintf("$%d OFFSET $%d", limitArg, limitArg+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result.Items, err = scanGroupUsageRankingRows(rows)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ListGroupUsage 使用同一条查询导出完整筛选结果，避免跨分页读取时用户排名变动。
func (r *UserMessageViewerRepository) ListGroupUsage(ctx context.Context, groupID int64, since time.Time) ([]*GroupUsageRanking, error) {
	query, args := groupUsageRankingQuery(groupID, since)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGroupUsageRankingRows(rows)
}

func groupUsageRankingQuery(groupID int64, since time.Time) (string, []any) {
	args := []any{since}
	usageWhere := "l.created_at >= $1"
	userWhere := "u.deleted_at IS NULL"
	if groupID > 0 {
		args = append(args, groupID)
		usageWhere += " AND l.group_id = $2"
		userWhere += " AND usage.user_id IS NOT NULL"
	}
	return `WITH usage AS (
		SELECT l.user_id, count(*) AS requests,
			coalesce(sum(l.input_tokens),0) AS input_tokens,
			coalesce(sum(l.output_tokens),0) AS output_tokens,
			coalesce(sum(l.cache_read_tokens),0) AS cache_read_tokens,
			coalesce(sum(l.total_cost),0)::float8 AS total_cost
		FROM usage_logs l WHERE ` + usageWhere + ` GROUP BY l.user_id
	)
	SELECT u.id, coalesce(u.username,''), coalesce(u.email,''),
		coalesce(usage.requests,0), coalesce(usage.input_tokens,0),
		coalesce(usage.output_tokens,0), coalesce(usage.cache_read_tokens,0), coalesce(usage.total_cost,0)
	FROM users u LEFT JOIN usage ON usage.user_id = u.id
	WHERE ` + userWhere + `
	ORDER BY coalesce(usage.total_cost,0) DESC, u.id ASC`, args
}

func scanGroupUsageRankingRows(rows *sql.Rows) ([]*GroupUsageRanking, error) {
	items := []*GroupUsageRanking{}
	for rows.Next() {
		item := &GroupUsageRanking{}
		if err := rows.Scan(&item.UserID, &item.Username, &item.UserEmail,
			&item.Requests, &item.InputTokens, &item.OutputTokens, &item.CacheRead, &item.TotalCost); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GroupNames 按分组 ID 集取 id+name（查不到的组返回 #id 占位）。
func (r *UserMessageViewerRepository) GroupNames(ctx context.Context, ids []int64) ([]*service.ViewerGroupInfo, error) {
	out := make([]*service.ViewerGroupInfo, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT id, coalesce(name, '') FROM groups WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		found[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		name, ok := found[id]
		if !ok {
			name = fmt.Sprintf("#%d", id)
		}
		out = append(out, &service.ViewerGroupInfo{ID: id, Name: name})
	}
	return out, nil
}
