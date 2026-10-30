package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUserUsageRankingPagination(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		groupID                         int64
		page, size                      int
		total                           int64
		wantPage, wantPages, wantOffset int
	}{
		{"全部用户第二页", 0, 2, 20, 106, 2, 6, 20},
		{"超出末页自动收敛", 0, 999, 20, 106, 6, 6, 100},
		{"分组分页保留范围", 20, 2, 10, 15, 2, 2, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			since := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
			count := mock.ExpectQuery(`SELECT count\(\*\) FROM users u WHERE u.deleted_at IS NULL`)
			if tc.groupID > 0 {
				count.WithArgs(since, tc.groupID)
			}
			count.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tc.total))
			pattern := `(?s)WITH usage AS.*FROM users u LEFT JOIN usage.*u.deleted_at IS NULL.*ORDER BY coalesce\(usage.total_cost,0\) DESC, u.id ASC.*LIMIT \$2 OFFSET \$3`
			if tc.groupID > 0 {
				pattern = `(?s)WITH usage AS.*l.group_id = \$2.*WHERE u.deleted_at IS NULL AND usage.user_id IS NOT NULL.*ORDER BY coalesce\(usage.total_cost,0\) DESC, u.id ASC.*LIMIT \$3 OFFSET \$4`
			}
			query := mock.ExpectQuery(pattern)
			if tc.groupID > 0 {
				query.WithArgs(since, tc.groupID, tc.size, tc.wantOffset)
			} else {
				query.WithArgs(since, tc.size, tc.wantOffset)
			}
			query.WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "email", "requests", "input", "output", "cache", "cost"}).
				AddRow(int64(21), "无用量用户", "", int64(0), int64(0), int64(0), int64(0), float64(0)))
			result, err := NewUserMessageViewerRepository(db).RankGroupUsage(context.Background(), tc.groupID, since, tc.page, tc.size)
			require.NoError(t, err)
			require.Equal(t, tc.total, result.Total)
			require.Equal(t, tc.wantPage, result.Page)
			require.Equal(t, tc.wantPages, result.Pages)
			require.Len(t, result.Items, 1)
			require.Zero(t, result.Items[0].Requests)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUserUsageRankingEmptyPage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	result, err := NewUserMessageViewerRepository(db).RankGroupUsage(context.Background(), 0, time.Now(), 12, 20)
	require.NoError(t, err)
	require.Equal(t, 1, result.Page)
	require.Equal(t, 1, result.Pages)
	require.NotNil(t, result.Items)
	require.Empty(t, result.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserUsageExportHasNoPaginationLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	since := time.Now()
	rows := sqlmock.NewRows([]string{"id", "username", "email", "requests", "input", "output", "cache", "cost"})
	for id := int64(1); id <= 125; id++ {
		rows.AddRow(id, "用户", "", 0, 0, 0, 0, 0)
	}
	mock.ExpectQuery(`(?s)WITH usage AS.*l.group_id = \$2.*usage.user_id IS NOT NULL.*ORDER BY coalesce\(usage.total_cost,0\) DESC, u.id ASC\s*$`).
		WithArgs(since, int64(20)).WillReturnRows(rows)
	result, err := NewUserMessageViewerRepository(db).ListGroupUsage(context.Background(), 20, since)
	require.NoError(t, err)
	require.Len(t, result, 125)
	require.Equal(t, int64(125), result[124].UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}
