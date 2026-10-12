package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func userMessageLogTestRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "created_at", "request_id", "user_id", "username", "user_email", "api_key_id", "api_key_name",
		"group_id", "group_name", "provider", "endpoint", "protocol", "model", "stream", "stage", "content", "content_length", "truncated",
	}).AddRow(int64(12), time.Now(), "username-test", int64(7), "当前用户名", "test@example.com", int64(2), "key",
		int64(20), "group", "openai", "/v1/responses", "openai_responses", "test-model", false, "http", "消息", 2, false)
}

func TestUserMessageLogList_ResolvesCurrentUsernameWithinAllowedGroups(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery(`SELECT count\(\*\) FROM user_message_logs WHERE 1=1 AND group_id IN \(\$1,\$2\)`).
		WithArgs(int64(20), int64(21)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT .*COALESCE\(u.username, m.username\).*left\(m.content,300\).*FROM \(SELECT \* FROM user_message_logs WHERE 1=1 AND group_id IN \(\$1,\$2\) ORDER BY id DESC LIMIT 20 OFFSET 0\) m LEFT JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL ORDER BY m.id DESC`).
		WithArgs(int64(20), int64(21)).WillReturnRows(userMessageLogTestRows())

	list, err := NewUserMessageLogRepository(db).List(context.Background(), &service.UserMessageLogFilter{AllowedGroupIDs: []int64{20, 21}})
	require.NoError(t, err)
	require.EqualValues(t, 1, list.Total)
	require.Len(t, list.Items, 1)
	require.Equal(t, "当前用户名", list.Items[0].Username)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserMessageLogGet_ResolvesCurrentUsername(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery(`(?s)SELECT .*COALESCE\(u.username, m.username\).* FROM user_message_logs m LEFT JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL WHERE m.id=\$1`).
		WithArgs(int64(12)).WillReturnRows(userMessageLogTestRows())

	item, err := NewUserMessageLogRepository(db).Get(context.Background(), 12)
	require.NoError(t, err)
	require.Equal(t, "当前用户名", item.Username)
	require.Equal(t, "消息", item.Content)
	require.NoError(t, mock.ExpectationsWereMet())
}
