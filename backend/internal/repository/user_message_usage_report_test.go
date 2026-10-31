package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestVisualGroupReportUsesOneScopedSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	from := time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT coalesce\(name,''\) FROM groups WHERE id=\$1 AND deleted_at IS NULL`).WithArgs(int64(20)).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("仅本分组"))
	columns := []string{"id", "name", "email", "requests", "input", "output", "read", "write", "actual", "standard"}
	users := sqlmock.NewRows(columns).AddRow(7, "组内用户", "u@example.invalid", 1, 100, 20, 30, 40, .1, .2).AddRow(8, "已绑定但未使用", "", 0, 0, 0, 0, 0, 0, 0)
	mock.ExpectQuery(`(?s)WITH usage AS.*l.group_id=\$1 AND l.created_at >= \$2 AND l.created_at < \$3.*k.group_id=\$1.*a.group_id=\$1.*s.group_id=\$1`).WithArgs(int64(20), from, to).WillReturnRows(users)
	detailColumns := []string{"id", "key", "requests", "input", "output", "read", "write", "actual", "standard"}
	mock.ExpectQuery(`(?s)SELECT l.user_id, to_char.*Asia/Shanghai.*l.group_id=\$1 AND l.created_at >= \$2 AND l.created_at < \$3`).WithArgs(int64(20), from, to).WillReturnRows(sqlmock.NewRows(detailColumns).AddRow(7, "2026-10-30", 1, 100, 20, 30, 40, .1, .2))
	mock.ExpectQuery(`(?s)SELECT l.user_id, coalesce\(nullif\(l.model.*l.group_id=\$1 AND l.created_at >= \$2 AND l.created_at < \$3`).WithArgs(int64(20), from, to).WillReturnRows(sqlmock.NewRows(detailColumns).AddRow(7, "gpt-6.1-sol", 1, 100, 20, 30, 40, .1, .2))
	mock.ExpectCommit()
	report, err := NewUserMessageViewerRepository(db).ReadGroupUsageReport(context.Background(), 20, from, to)
	require.NoError(t, err)
	require.Equal(t, int64(20), report.GroupID)
	require.Equal(t, from, report.StartTime)
	require.Equal(t, to, report.EndTime)
	require.Len(t, report.Users, 2)
	require.Zero(t, report.Users[1].Requests)
	require.Len(t, report.Daily, 1)
	require.Len(t, report.Models, 1)
	require.Equal(t, int64(40), report.Users[0].CacheWrite)
	require.InDelta(t, .1, report.Users[0].ActualCost, 1e-12)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVisualGroupReportDoesNotReadWithoutGroup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	_, err = NewUserMessageViewerRepository(db).ReadGroupUsageReport(context.Background(), 0, time.Now(), time.Now())
	require.ErrorIs(t, err, service.ErrUsageReportGroupRequired)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVisualGroupReportMissingGroupRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT coalesce`).WithArgs(int64(999)).WillReturnRows(sqlmock.NewRows([]string{"name"}))
	mock.ExpectRollback()
	_, err = NewUserMessageViewerRepository(db).ReadGroupUsageReport(context.Background(), 999, time.Now(), time.Now())
	require.ErrorIs(t, err, service.ErrUsageReportGroupNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
