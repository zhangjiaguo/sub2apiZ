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

// RankGroupUsage 按分组聚合 usage_logs 的用户用量排行（近 N 天）。
func (r *UserMessageViewerRepository) RankGroupUsage(ctx context.Context, groupID int64, since time.Time, limit int) ([]*GroupUsageRanking, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT l.user_id, coalesce(max(u.username),''), coalesce(max(u.email),''),
			count(*), coalesce(sum(l.input_tokens),0), coalesce(sum(l.output_tokens),0),
			coalesce(sum(l.cache_read_tokens),0), coalesce(sum(l.total_cost),0)::float8
		FROM usage_logs l
		JOIN users u ON u.id = l.user_id
		WHERE l.group_id = $1 AND l.created_at >= $2 AND l.user_id IS NOT NULL
		GROUP BY l.user_id
		ORDER BY sum(l.total_cost) DESC NULLS LAST
		LIMIT $3`, groupID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*GroupUsageRanking{}
	for rows.Next() {
		item := &GroupUsageRanking{}
		if err := rows.Scan(&item.UserID, &item.Username, &item.UserEmail,
			&item.Requests, &item.InputTokens, &item.OutputTokens, &item.CacheRead, &item.TotalCost); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
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
