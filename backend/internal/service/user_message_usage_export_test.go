package service

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserUsageCSVIncludesAllUsersAndTotals(t *testing.T) {
	rows := make([]*GroupUsageRanking, 125)
	for i := range rows {
		rows[i] = &GroupUsageRanking{UserID: int64(i + 1), Username: "中文用户", Requests: 1, InputTokens: 10, OutputTokens: 2, CacheRead: 3, TotalCost: 0.1}
	}
	rows[0].Username = "  =1+1"
	rows[0].UserEmail = "带逗号,引号\"和\n换行"
	data, err := buildGroupUsageCSV(rows, 0, 7)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(data), "\xef\xbb\xbf"))
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\xef\xbb\xbf"))).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 127)
	require.Equal(t, "用户ID", records[0][1])
	require.Equal(t, "'  =1+1", records[1][2])
	require.Equal(t, rows[0].UserEmail, records[1][3])
	require.Equal(t, "125", records[125][1])
	require.Equal(t, []string{"合计（125人）", "", "", "", "125", "1250", "250", "375", "12.50000000", "全部分组", "7"}, records[126])
}

func TestUserUsageCSVTextCellsAreNotFormulas(t *testing.T) {
	for _, value := range []string{"=1+1", "+SUM(A1)", "-1+1", "@SUM(A1)", "\t=1+1", "\ufeff=1+1"} {
		require.Equal(t, "'"+value, usageCSVText(value))
	}
	require.Equal(t, "正常用户名", usageCSVText("正常用户名"))
	require.Equal(t, "name@example.com", usageCSVText("name@example.com"))
}
