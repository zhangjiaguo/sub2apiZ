// Package repository —— user_message_logs 数据访问（实现 service.UserMessageLogRepository）。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type UserMessageLogRepository struct {
	db *sql.DB
}

func NewUserMessageLogRepository(db *sql.DB) *UserMessageLogRepository {
	return &UserMessageLogRepository{db: db}
}

const userMessageLogColumns = `id,created_at,request_id,user_id,username,user_email,api_key_id,api_key_name,
	group_id,group_name,provider,endpoint,protocol,model,stream,stage,content,content_length,truncated`

// InsertBatch 批量写入（单事务）。
func (r *UserMessageLogRepository) InsertBatch(ctx context.Context, logs []*service.UserMessageLog) error {
	if len(logs) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO user_message_logs (
			request_id,user_id,username,user_email,api_key_id,api_key_name,
			group_id,group_name,provider,endpoint,protocol,model,stream,stage,
			content,content_length,truncated
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, log := range logs {
		var userID, apiKeyID, groupID any
		if log.UserID > 0 {
			userID = log.UserID
		}
		if log.APIKeyID > 0 {
			apiKeyID = log.APIKeyID
		}
		if log.GroupID > 0 {
			groupID = log.GroupID
		}
		if _, err := stmt.ExecContext(ctx,
			log.RequestID, userID, log.Username, log.UserEmail, apiKeyID, log.APIKeyName,
			groupID, log.GroupName, log.Provider, log.Endpoint, log.Protocol, log.Model, log.Stream, log.Stage,
			log.Content, log.ContentLength, log.Truncated,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// List 按条件分页查询（content 精简为前 300 字符预览，全文走 Get）。
func (r *UserMessageLogRepository) List(ctx context.Context, filter *service.UserMessageLogFilter) (*service.UserMessageLogList, error) {
	where, args := userMessageLogWhere(filter)
	var total int64
	if err := r.db.QueryRowContext(ctx,
		"SELECT count(*) FROM user_message_logs "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	page, pageSize := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	query := fmt.Sprintf(
		"SELECT id,created_at,request_id,user_id,username,user_email,api_key_id,api_key_name,group_id,group_name,provider,endpoint,protocol,model,stream,stage,left(content,300),content_length,truncated FROM user_message_logs %s ORDER BY id DESC LIMIT %d OFFSET %d",
		where, pageSize, (page-1)*pageSize)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := &service.UserMessageLogList{Items: []*service.UserMessageLog{}, Total: total, Page: page, PageSize: pageSize}
	for rows.Next() {
		item, err := scanUserMessageLog(rows)
		if err != nil {
			return nil, err
		}
		list.Items = append(list.Items, item)
	}
	return list, rows.Err()
}

// Get 按 ID 返回全文记录。
func (r *UserMessageLogRepository) Get(ctx context.Context, id int64) (*service.UserMessageLog, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+userMessageLogColumns+" FROM user_message_logs WHERE id=$1", id)
	item, err := scanUserMessageLog(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUserMessageLogNotFound
	}
	return item, err
}

// CleanupBefore 删除早于 cutoff 的记录，返回删除行数。
func (r *UserMessageLogRepository) CleanupBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, "DELETE FROM user_message_logs WHERE created_at < $1", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func userMessageLogWhere(filter *service.UserMessageLogFilter) (string, []any) {
	conds := []string{"1=1"}
	var args []any
	if filter.UserID != nil {
		args = append(args, *filter.UserID)
		conds = append(conds, fmt.Sprintf("user_id=$%d", len(args)))
	}
	if filter.GroupID != nil {
		args = append(args, *filter.GroupID)
		conds = append(conds, fmt.Sprintf("group_id=$%d", len(args)))
	}
	if filter.Model != "" {
		args = append(args, filter.Model)
		conds = append(conds, fmt.Sprintf("model=$%d", len(args)))
	}
	if filter.Query != "" {
		args = append(args, "%"+strings.ToLower(filter.Query)+"%")
		conds = append(conds, fmt.Sprintf("LOWER(content) LIKE $%d", len(args)))
	}
	if filter.StartTime != nil {
		args = append(args, *filter.StartTime)
		conds = append(conds, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if filter.EndTime != nil {
		args = append(args, *filter.EndTime)
		conds = append(conds, fmt.Sprintf("created_at < $%d", len(args)))
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

type userMessageLogRow interface{ Scan(dest ...any) error }

func scanUserMessageLog(row userMessageLogRow) (*service.UserMessageLog, error) {
	item := &service.UserMessageLog{}
	var userID, apiKeyID, groupID sql.NullInt64
	err := row.Scan(
		&item.ID, &item.CreatedAt, &item.RequestID, &userID, &item.Username, &item.UserEmail,
		&apiKeyID, &item.APIKeyName, &groupID, &item.GroupName, &item.Provider, &item.Endpoint,
		&item.Protocol, &item.Model, &item.Stream, &item.Stage, &item.Content, &item.ContentLength, &item.Truncated,
	)
	if err != nil {
		return nil, err
	}
	item.UserID = userID.Int64
	item.APIKeyID = apiKeyID.Int64
	item.GroupID = groupID.Int64
	return item, nil
}
