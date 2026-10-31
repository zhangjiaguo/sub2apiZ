package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ExportGroupUsage 仅导出用量字段，不包含用户消息、密钥或其他用户资料。
func (s *UserMessageLogService) ExportGroupUsage(ctx context.Context, groupID int64, days int) ([]byte, string, error) {
	if groupID <= 0 {
		return nil, "", ErrUsageReportGroupRequired
	}
	if days <= 0 || days > 90 {
		days = 7
	}
	now := time.Now()
	rows, err := s.viewerRepo.ListGroupUsage(ctx, groupID, now.AddDate(0, 0, -days))
	if err != nil {
		return nil, "", err
	}
	data, err := buildGroupUsageCSV(rows, groupID, days)
	if err != nil {
		return nil, "", err
	}
	scope := "全部分组"
	if groupID > 0 {
		scope = fmt.Sprintf("分组%d", groupID)
	}
	stamp := now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("20060102-150405")
	return data, fmt.Sprintf("用户用量_%s_近%d天_%s.csv", scope, days, stamp), nil
}

func buildGroupUsageCSV(rows []*GroupUsageRanking, groupID int64, days int) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteString("\xef\xbb\xbf") // Excel 直接打开时识别 UTF-8 中文。
	writer := csv.NewWriter(&buffer)
	writer.UseCRLF = true
	if err := writer.Write([]string{"序号", "用户ID", "用户名", "邮箱", "请求数", "输入Token", "输出Token", "缓存读取Token", "费用（USD）", "统计分组", "统计天数"}); err != nil {
		return nil, err
	}
	scope := "全部分组"
	if groupID > 0 {
		scope = strconv.FormatInt(groupID, 10)
	}
	var requests, input, output, cache int64
	var cost float64
	for i, row := range rows {
		record := []string{strconv.Itoa(i + 1), strconv.FormatInt(row.UserID, 10), usageCSVText(row.Username), usageCSVText(row.UserEmail),
			strconv.FormatInt(row.Requests, 10), strconv.FormatInt(row.InputTokens, 10), strconv.FormatInt(row.OutputTokens, 10),
			strconv.FormatInt(row.CacheRead, 10), strconv.FormatFloat(row.TotalCost, 'f', 8, 64), scope, strconv.Itoa(days)}
		if err := writer.Write(record); err != nil {
			return nil, err
		}
		requests += row.Requests
		input += row.InputTokens
		output += row.OutputTokens
		cache += row.CacheRead
		cost += row.TotalCost
	}
	if err := writer.Write([]string{fmt.Sprintf("合计（%d人）", len(rows)), "", "", "", strconv.FormatInt(requests, 10),
		strconv.FormatInt(input, 10), strconv.FormatInt(output, 10), strconv.FormatInt(cache, 10),
		strconv.FormatFloat(cost, 'f', 8, 64), scope, strconv.Itoa(days)}); err != nil {
		return nil, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// 用户名和邮箱按文本导出，防止表格软件将用户输入解释为公式。
func usageCSVText(value string) string {
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' })
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
