package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// 本文件定义分组用量报表的共同数据快照。Excel与HTML读取同一份结果，
// 所有用户、每日和模型数据均已在仓储层限定到单个分组与同一统计区间。
var (
	ErrUsageReportGroupRequired = errors.New("请先选择一个具体分组")
	ErrUsageReportGroupNotFound = errors.New("分组不存在")
	ErrUsageReportFormat        = errors.New("不支持的报表格式")
)

// UsageReportMetrics 的四种Token量互不重叠，费用保留账本中的美元值。
type UsageReportMetrics struct {
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read_tokens"`
	CacheWrite   int64   `json:"cache_write_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	ActualCost   float64 `json:"actual_cost"`
	StandardCost float64 `json:"standard_cost"`
}

func (m *UsageReportMetrics) normalize() {
	m.TotalTokens = m.InputTokens + m.OutputTokens + m.CacheRead + m.CacheWrite
}

func (m *UsageReportMetrics) add(row UsageReportMetrics) {
	m.Requests += row.Requests
	m.InputTokens += row.InputTokens
	m.OutputTokens += row.OutputTokens
	m.CacheRead += row.CacheRead
	m.CacheWrite += row.CacheWrite
	m.ActualCost += row.ActualCost
	m.StandardCost += row.StandardCost
	m.normalize()
}

type UsageReportUser struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	UserEmail string `json:"user_email"`
	UsageReportMetrics
}

// Label 在重名时仍保留稳定ID；完整姓名与邮箱另外保存在用户明细中。
func (u UsageReportUser) Label() string {
	name := strings.TrimSpace(u.Username)
	if name == "" {
		name = u.UserEmail
	}
	if name == "" {
		name = "用户"
	}
	return fmt.Sprintf("用户%d %s", u.UserID, name)
}

type UsageReportDay struct {
	UserID int64  `json:"user_id"`
	Date   string `json:"date"`
	UsageReportMetrics
}

type UsageReportModel struct {
	UserID int64  `json:"user_id"`
	Model  string `json:"model"`
	UsageReportMetrics
}

// GroupUsageReport 仅包含所选分组的汇总与身份标签，不包含密钥、消息正文或上游成本。
type GroupUsageReport struct {
	GroupID     int64              `json:"group_id"`
	GroupName   string             `json:"group_name"`
	StartTime   time.Time          `json:"start_time"`
	EndTime     time.Time          `json:"end_time"`
	GeneratedAt time.Time          `json:"generated_at"`
	Days        int                `json:"days"`
	Users       []UsageReportUser  `json:"users"`
	Daily       []UsageReportDay   `json:"daily"`
	Models      []UsageReportModel `json:"models"`
	Summary     UsageReportMetrics `json:"summary"`
	ActiveUsers int                `json:"active_users"`
}

// prepare 统一派生总量与排序。缺少用量表示绑定成员真实的零用量，不伪造请求记录。
func (r *GroupUsageReport) prepare() {
	r.Summary = UsageReportMetrics{}
	r.ActiveUsers = 0
	for i := range r.Users {
		r.Users[i].normalize()
		r.Summary.add(r.Users[i].UsageReportMetrics)
		if r.Users[i].Requests > 0 {
			r.ActiveUsers++
		}
	}
	for i := range r.Daily {
		r.Daily[i].normalize()
	}
	for i := range r.Models {
		r.Models[i].normalize()
	}
	sort.SliceStable(r.Users, func(i, j int) bool {
		if r.Users[i].ActualCost != r.Users[j].ActualCost {
			return r.Users[i].ActualCost > r.Users[j].ActualCost
		}
		return r.Users[i].UserID < r.Users[j].UserID
	})
	if r.Users == nil {
		r.Users = []UsageReportUser{}
	}
	if r.Daily == nil {
		r.Daily = []UsageReportDay{}
	}
	if r.Models == nil {
		r.Models = []UsageReportModel{}
	}
}

// ExportGroupUsageReport 要求具体分组。调用者完成管理员/查看者鉴权后才能进入本函数。
// 起止时刻只计算一次，仓储在只读一致快照中聚合，避免三种明细跨时点产生差额。
func (s *UserMessageLogService) ExportGroupUsageReport(ctx context.Context, groupID int64, days int, format string) ([]byte, string, string, error) {
	if groupID <= 0 {
		return nil, "", "", ErrUsageReportGroupRequired
	}
	if format == "csv" {
		data, filename, err := s.ExportGroupUsage(ctx, groupID, days)
		return data, filename, "text/csv; charset=utf-8", err
	}
	if format != "xlsx" && format != "html" {
		return nil, "", "", ErrUsageReportFormat
	}
	if days <= 0 || days > 90 {
		days = 7
	}
	now := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600))
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	report, err := s.viewerRepo.ReadGroupUsageReport(ctx, groupID, now.AddDate(0, 0, -days), now)
	if err != nil {
		return nil, "", "", err
	}
	report.GeneratedAt, report.Days = now, days
	report.prepare()
	var data []byte
	contentType := "text/html; charset=utf-8"
	if format == "xlsx" {
		data, err = buildGroupUsageXLSX(report)
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	} else {
		data, err = buildGroupUsageHTML(report)
	}
	if err != nil {
		return nil, "", "", err
	}
	filename := fmt.Sprintf("用户用量_分组%d_近%d天_%s.%s", groupID, days, now.Format("20060102-150405"), format)
	return data, filename, contentType, nil
}
