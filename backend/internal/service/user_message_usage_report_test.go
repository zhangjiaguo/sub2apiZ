package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// 固定数据覆盖超过一页的用户、零用量成员、缓存读写和两种费用，便于独立对账。
func usageVisualFixture() *GroupUsageReport {
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	end := time.Date(2026, 10, 31, 12, 0, 0, 0, zone)
	r := &GroupUsageReport{GroupID: 20, GroupName: "团队用量", StartTime: end.AddDate(0, 0, -7), EndTime: end, GeneratedAt: end, Days: 7}
	for i := 1; i <= 25; i++ {
		u := UsageReportUser{UserID: int64(i), Username: "测试用户" + strconv.Itoa(i), UserEmail: "user" + strconv.Itoa(i) + "@example.invalid"}
		if i <= 24 {
			m := UsageReportMetrics{Requests: 2, InputTokens: int64(i * 1000), OutputTokens: int64(i * 100), CacheRead: int64(i * 200), CacheWrite: int64(i * 50), ActualCost: float64(i) * 0.01, StandardCost: float64(i) * 0.02}
			u.UsageReportMetrics = m
			r.Daily = append(r.Daily, UsageReportDay{UserID: u.UserID, Date: "2026-10-30", UsageReportMetrics: m})
			model := "gpt-6.1-sol"
			if i%2 == 0 {
				model = "glm-5.3"
			}
			r.Models = append(r.Models, UsageReportModel{UserID: u.UserID, Model: model, UsageReportMetrics: m})
		}
		r.Users = append(r.Users, u)
	}
	r.prepare()
	return r
}

func TestGroupUsageReportReconcilesAllGrains(t *testing.T) {
	r := usageVisualFixture()
	require.Len(t, r.Users, 25)
	require.Equal(t, 24, r.ActiveUsers)
	require.Equal(t, int64(48), r.Summary.Requests)
	require.Equal(t, int64(405000), r.Summary.TotalTokens)
	require.InDelta(t, 3, r.Summary.ActualCost, 1e-12)
	require.InDelta(t, 6, r.Summary.StandardCost, 1e-12)
	var daily, model UsageReportMetrics
	for _, row := range r.Daily {
		daily.add(row.UsageReportMetrics)
	}
	for _, row := range r.Models {
		model.add(row.UsageReportMetrics)
	}
	require.Equal(t, r.Summary.TotalTokens, daily.TotalTokens)
	require.Equal(t, r.Summary.TotalTokens, model.TotalTokens)
	require.InDelta(t, r.Summary.ActualCost, daily.ActualCost, 1e-12)
	require.Zero(t, r.Users[len(r.Users)-1].Requests)
}

func TestGroupUsageHTMLDoesNotExecuteUserContent(t *testing.T) {
	r := usageVisualFixture()
	r.GroupName = `团队</title><script>alert(1)</script>`
	r.Users[0].Username = `</script><script>alert("用户")</script>`
	data, err := buildGroupUsageHTML(r)
	require.NoError(t, err)
	text := string(data)
	require.NotContains(t, text, r.Users[0].Username)
	require.NotContains(t, text, `团队</title>`)
	require.Contains(t, text, `\u003c/script\u003e`)
	require.Contains(t, text, `connect-src 'none'`)
	start := strings.Index(text, `<script id="report-data" type="application/json">`)
	require.GreaterOrEqual(t, start, 0)
	jsonPart := strings.SplitN(text[start:], ">", 2)[1]
	jsonPart = strings.SplitN(jsonPart, "</script>", 2)[0]
	var decoded GroupUsageReport
	require.NoError(t, json.Unmarshal([]byte(jsonPart), &decoded))
	require.Len(t, decoded.Users, 25)
	require.Equal(t, r.Users[0].Username, decoded.Users[0].Username)
}

func TestGroupUsageXLSXNativeChartsAndPersonalSelection(t *testing.T) {
	r := usageVisualFixture()
	r.Users[0].Username = "=1+1"
	data, err := buildGroupUsageXLSX(r)
	require.NoError(t, err)
	f, err := excelize.OpenReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	require.Equal(t, []string{usageOverview, usagePersonal, usageUsers, usageDaily, usageModels}, f.GetSheetList())
	rows, err := f.GetRows(usageUsers)
	require.NoError(t, err)
	require.Len(t, rows, 31)
	formula, err := f.GetCellFormula(usageUsers, "B7")
	require.NoError(t, err)
	require.Empty(t, formula)
	value, err := f.GetCellValue(usageUsers, "B7")
	require.NoError(t, err)
	require.Equal(t, "=1+1", value)
	check := func(sheet, cell string, expected float64) {
		raw, e := f.CalcCellValue(sheet, cell, excelize.Options{RawCellValue: true})
		require.NoError(t, e)
		got, e := strconv.ParseFloat(raw, 64)
		require.NoError(t, e)
		require.InDelta(t, expected, got, 1e-8)
	}
	check(usageOverview, "A7", 48)
	check(usageOverview, "E7", 405000)
	check(usageOverview, "I7", 24)
	check(usageOverview, "M7", 3)
	check(usagePersonal, "A7", 2)
	check(usagePersonal, "E7", 32400)
	check(usagePersonal, "I7", .24)
	// 修改实际选择器后重算相同分析，不使用第二套硬编码个人数据。
	require.NoError(t, f.SetCellStr(usagePersonal, "B4", "用户25 测试用户25"))
	check(usagePersonal, "A7", 0)
	check(usagePersonal, "E7", 0)
	check(usagePersonal, "I7", 0)
	require.NoError(t, f.SetCellStr(usagePersonal, "B4", "用户1 测试用户1"))
	check(usagePersonal, "A7", 2)
	check(usagePersonal, "E7", 1350)
	check(usagePersonal, "I7", .01)
	validations, err := f.GetDataValidations(usagePersonal)
	require.NoError(t, err)
	require.Len(t, validations, 1)
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	charts := 0
	for _, entry := range archive.File {
		if strings.HasPrefix(entry.Name, "xl/charts/chart") && strings.HasSuffix(entry.Name, ".xml") {
			charts++
			stream, e := entry.Open()
			require.NoError(t, e)
			xml, e := io.ReadAll(stream)
			_ = stream.Close()
			require.NoError(t, e)
			require.Contains(t, string(xml), "numCache")
			require.NotContains(t, string(xml), "#REF!")
		}
	}
	require.Equal(t, 5, charts)
}

func TestGroupUsageXLSXLongPeriodAndEmptyGroup(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(strconv.FormatBool(empty), func(t *testing.T) {
			r := usageVisualFixture()
			r.StartTime = r.EndTime.AddDate(0, 0, -90)
			r.Days = 90
			if empty {
				r.Users = nil
				r.Daily = nil
				r.Models = nil
				r.prepare()
			}
			data, err := buildGroupUsageXLSX(r)
			require.NoError(t, err)
			f, err := excelize.OpenReader(bytes.NewReader(data))
			require.NoError(t, err)
			defer func() { _ = f.Close() }()
			raw, err := f.CalcCellValue(usageOverview, "E7", excelize.Options{RawCellValue: true})
			require.NoError(t, err)
			value, err := strconv.ParseFloat(raw, 64)
			require.NoError(t, err)
			require.Equal(t, float64(r.Summary.TotalTokens), value)
			date, err := f.GetCellValue(usagePersonal, "A124")
			require.NoError(t, err)
			require.Equal(t, "2026-10-31", date)
		})
	}
}

func TestGroupUsageExportRejectsUnscopedReports(t *testing.T) {
	s := &UserMessageLogService{}
	for _, format := range []string{"csv", "xlsx", "html"} {
		_, _, _, err := s.ExportGroupUsageReport(context.Background(), 0, 7, format)
		require.ErrorIs(t, err, ErrUsageReportGroupRequired)
	}
	_, _, _, err := s.ExportGroupUsageReport(context.Background(), 20, 7, "exe")
	require.ErrorIs(t, err, ErrUsageReportFormat)
}

// 仅在显式设置输出目录时生成无真实用户信息的验收文件，不参与生产运行。
func TestGroupUsageVisualArtifacts(t *testing.T) {
	folder := os.Getenv("USAGE_REPORT_TEST_OUTPUT")
	if folder == "" {
		t.Skip("未请求生成演示报表")
	}
	require.NoError(t, os.MkdirAll(folder, 0o700))
	r := usageVisualFixture()
	for _, format := range []string{"xlsx", "html"} {
		var data []byte
		var err error
		if format == "xlsx" {
			data, err = buildGroupUsageXLSX(r)
		} else {
			data, err = buildGroupUsageHTML(r)
		}
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(folder, "分组用量演示."+format), data, 0o600))
	}
}
