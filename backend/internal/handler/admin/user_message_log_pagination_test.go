package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type usageAdminPaginationRepo struct {
	service.UserMessageViewerRepository
	groupID        int64
	page, pageSize int
	called         bool
}

func (r *usageAdminPaginationRepo) RankGroupUsage(_ context.Context, groupID int64, _ time.Time, page, pageSize int) (*service.GroupUsageRankingList, error) {
	r.groupID, r.page, r.pageSize, r.called = groupID, page, pageSize, true
	return &service.GroupUsageRankingList{Items: []*service.GroupUsageRanking{}, Total: 92, Page: page, PageSize: pageSize, Pages: 5}, nil
}

func TestAdminUserUsagePagination(t *testing.T) {
	for _, tc := range []struct {
		query            string
		status           int
		groupID          int64
		page, size, days int
	}{
		{"?page=2&page_size=20", 200, 0, 2, 20, 7},
		{"?group_id=20&days=30&page=1&page_size=50", 200, 20, 1, 50, 30},
		{"?days=999&page_size=1000", 200, 0, 1, 100, 7},
		{"?group_id=-1", 400, 0, 0, 0, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			repo := &usageAdminPaginationRepo{}
			handler := NewUserMessageLogHandler(service.ProvideUserMessageLogService(nil, repo))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("GET", "/api/v1/admin/user-message-logs/usage-summary"+tc.query, nil)
			handler.ListUsageRanking(c)
			require.Equal(t, tc.status, recorder.Code)
			if tc.status == 200 {
				require.True(t, repo.called)
				require.Equal(t, tc.groupID, repo.groupID)
				require.Equal(t, tc.page, repo.page)
				require.Equal(t, tc.size, repo.pageSize)
				require.Contains(t, recorder.Body.String(), `"total":92`)
				var body struct {
					Data service.GroupUsageRankingList `json:"data"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
				require.Equal(t, tc.days, body.Data.Days)
			} else {
				require.False(t, repo.called)
			}
		})
	}
}

func (r *usageAdminPaginationRepo) ReadGroupUsageReport(_ context.Context, groupID int64, from, to time.Time) (*service.GroupUsageReport, error) {
	r.called, r.groupID = true, groupID
	return &service.GroupUsageReport{GroupID: groupID, GroupName: "测试组", StartTime: from, EndTime: to}, nil
}

func TestAdminUserUsageExportRequiresOneGroup(t *testing.T) {
	for _, format := range []string{"xlsx", "html", "csv"} {
		for _, query := range []string{"", "&group_id=0", "&group_id=-1"} {
			repo := &usageAdminPaginationRepo{}
			handler := NewUserMessageLogHandler(service.ProvideUserMessageLogService(nil, repo))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("GET", "/export?format="+format+query, nil)
			handler.ExportUsageRanking(c)
			require.Equal(t, 400, recorder.Code)
			require.False(t, repo.called)
		}
	}
	for _, format := range []string{"xlsx", "html", "exe"} {
		repo := &usageAdminPaginationRepo{}
		handler := NewUserMessageLogHandler(service.ProvideUserMessageLogService(nil, repo))
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("GET", "/export?group_id=20&format="+format, nil)
		handler.ExportUsageRanking(c)
		if format == "exe" {
			require.Equal(t, 400, recorder.Code)
			require.False(t, repo.called)
		} else {
			require.Equal(t, 200, recorder.Code)
			require.True(t, repo.called)
			require.EqualValues(t, 20, repo.groupID)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		}
	}
}
