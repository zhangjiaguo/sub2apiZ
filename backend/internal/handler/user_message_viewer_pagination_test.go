package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type usageViewerPaginationRepo struct {
	service.UserMessageViewerRepository
	called bool
}

func (r *usageViewerPaginationRepo) GroupsForUser(context.Context, int64) ([]int64, error) {
	return []int64{20}, nil
}

func (r *usageViewerPaginationRepo) RankGroupUsage(_ context.Context, groupID int64, _ time.Time, page, pageSize int) (*service.GroupUsageRankingList, error) {
	r.called = true
	return &service.GroupUsageRankingList{Items: []*service.GroupUsageRanking{}, GroupID: groupID, Page: page, PageSize: pageSize, Pages: 1}, nil
}

func (r *usageViewerPaginationRepo) ListGroupUsage(_ context.Context, _ int64, _ time.Time) ([]*service.GroupUsageRanking, error) {
	r.called = true
	return []*service.GroupUsageRanking{{UserID: 42, Requests: 1}}, nil
}

func (r *usageViewerPaginationRepo) ReadGroupUsageReport(_ context.Context, groupID int64, from, to time.Time) (*service.GroupUsageReport, error) {
	r.called = true
	return &service.GroupUsageReport{GroupID: groupID, GroupName: "授权分组", StartTime: from, EndTime: to, Users: []service.UsageReportUser{{UserID: 42, Username: "本组用户"}}}, nil
}

func TestViewerVisualReportFormatsKeepAuthorization(t *testing.T) {
	for _, format := range []string{"xlsx", "html"} {
		for _, group := range []string{"", "0", "57", "20"} {
			t.Run(format+"/"+group, func(t *testing.T) {
				repo := &usageViewerPaginationRepo{}
				h := NewUserMessageViewerHandler(service.ProvideUserMessageLogService(nil, repo))
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest("GET", "/export?format="+format+"&group_id="+group, nil)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
				h.ExportUsageRanking(c)
				status := 400
				if group == "57" {
					status = 403
				}
				if group == "20" {
					status = 200
				}
				require.Equal(t, status, recorder.Code)
				require.Equal(t, group == "20", repo.called)
				if status == 200 {
					require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
					if format == "xlsx" {
						require.Contains(t, recorder.Header().Get("Content-Type"), "spreadsheetml")
						require.True(t, strings.HasPrefix(recorder.Body.String(), "PK"))
					} else {
						require.Contains(t, recorder.Body.String(), "授权分组")
					}
				}
			})
		}
	}
}

func TestViewerUsageExportKeepsGroupAuthorization(t *testing.T) {
	for _, tc := range []struct {
		query  string
		status int
		called bool
	}{
		{"?days=7", 400, false},
		{"?group_id=57", 403, false},
		{"?group_id=20", 200, true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			repo := &usageViewerPaginationRepo{}
			h := NewUserMessageViewerHandler(service.ProvideUserMessageLogService(nil, repo))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("GET", "/api/v1/user-message-logs/usage-summary/export"+tc.query, nil)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			h.ExportUsageRanking(c)
			require.Equal(t, tc.status, recorder.Code)
			require.Equal(t, tc.called, repo.called)
			if tc.called {
				require.Equal(t, "text/csv; charset=utf-8", recorder.Header().Get("Content-Type"))
				require.Contains(t, recorder.Header().Get("Content-Disposition"), "attachment")
				require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
				require.Contains(t, recorder.Body.String(), "合计（1人）")
			}
		})
	}
}

func TestViewerUsagePaginationKeepsGroupAuthorization(t *testing.T) {
	for _, tc := range []struct {
		query  string
		status int
		called bool
	}{
		{"?page=2", 400, false},
		{"?group_id=0&page=2", 400, false},
		{"?group_id=57&page=2", 403, false},
		{"?group_id=20&page=2&page_size=10", 200, true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			repo := &usageViewerPaginationRepo{}
			handler := NewUserMessageViewerHandler(service.ProvideUserMessageLogService(nil, repo))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("GET", "/api/v1/user-message-logs/usage-summary"+tc.query, nil)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			handler.UsageRanking(c)
			require.Equal(t, tc.status, recorder.Code)
			require.Equal(t, tc.called, repo.called)
			if tc.called {
				require.Contains(t, recorder.Body.String(), `"page":2`)
				require.Contains(t, recorder.Body.String(), `"page_size":10`)
			}
		})
	}
}
