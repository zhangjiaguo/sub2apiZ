package service

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/xuri/excelize/v2"
)

// Excel使用原生单元格、公式、数据验证与图表。流式Cell同时保存公式和初始计算值，
// 支持初次预览；Excel按自动计算模式在用户切换个人选择器后重新计算同一套分析。
const (
	usageOverview = "分组总览"
	usagePersonal = "个人分析"
	usageUsers    = "用户明细"
	usageDaily    = "每日明细"
	usageModels   = "模型明细"
	usageDataRow  = 7
)

type usageExcelSheet struct {
	name    string
	rows    map[int]map[int]excelize.Cell
	merges  [][2]string
	heights map[int]float64
	widths  map[int]float64
}

type usageExcelBuilder struct {
	f       *excelize.File
	sheets  map[string]*usageExcelSheet
	styles  map[string]int
	pending []usageExcelChart
	err     error
}

func (b *usageExcelBuilder) check(err error) {
	if b.err == nil {
		b.err = err
	}
}

func (b *usageExcelBuilder) put(sheet string, row, col int, value any, style string) {
	s := b.sheets[sheet]
	if s.rows[row] == nil {
		s.rows[row] = map[int]excelize.Cell{}
	}
	s.rows[row][col] = excelize.Cell{Value: value, StyleID: b.styles[style]}
}

func (b *usageExcelBuilder) formula(sheet string, row, col int, formula string, cached any, style string) {
	b.put(sheet, row, col, cached, style)
	cell := b.sheets[sheet].rows[row][col]
	cell.Formula = formula
	b.sheets[sheet].rows[row][col] = cell
}

func (b *usageExcelBuilder) merged(sheet, from, to string, value any, style string) {
	c1, r1, err := excelize.CellNameToCoordinates(from)
	b.check(err)
	c2, r2, err := excelize.CellNameToCoordinates(to)
	b.check(err)
	for r := r1; r <= r2; r++ {
		for c := c1; c <= c2; c++ {
			b.put(sheet, r, c, "", style)
		}
	}
	b.put(sheet, r1, c1, value, style)
	b.sheets[sheet].merges = append(b.sheets[sheet].merges, [2]string{from, to})
}

func (b *usageExcelBuilder) header(sheet string, columns []string) {
	for i, title := range columns {
		b.put(sheet, 6, i+1, title, "tableHeader")
	}
	b.sheets[sheet].heights[6] = 30
}

func (b *usageExcelBuilder) title(sheet, title, subtitle string) {
	b.merged(sheet, "A1", "P2", title, "title")
	b.merged(sheet, "A3", "P3", subtitle, "muted")
	b.sheets[sheet].heights[1] = 25
	b.sheets[sheet].heights[2] = 25
	b.sheets[sheet].heights[3] = 28
}

func (b *usageExcelBuilder) createStyles() {
	base := func(size float64, color string, bold bool) *excelize.Font {
		return &excelize.Font{Family: "Microsoft YaHei", Size: size, Color: color, Bold: bold}
	}
	formats := map[string]*excelize.Style{
		"text":        {Font: base(10, "334155", false), Alignment: &excelize.Alignment{Vertical: "center"}},
		"number":      {Font: base(10, "334155", false), NumFmt: 3, Alignment: &excelize.Alignment{Horizontal: "right", Vertical: "center"}},
		"money":       {Font: base(10, "0F766E", false), CustomNumFmt: usageStringPtr(`"$"#,##0.00######`), Alignment: &excelize.Alignment{Horizontal: "right", Vertical: "center"}},
		"date":        {Font: base(10, "64748B", false), CustomNumFmt: usageStringPtr("yyyy-mm-dd"), Alignment: &excelize.Alignment{Vertical: "center"}},
		"title":       {Font: base(22, "172B35", true), Alignment: &excelize.Alignment{Vertical: "center"}},
		"muted":       {Font: base(10, "64748B", false), Alignment: &excelize.Alignment{Vertical: "center", WrapText: true}},
		"tableHeader": {Font: base(10, "0F766E", true), Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"E6F5EF"}}, Alignment: &excelize.Alignment{Vertical: "center"}, Border: []excelize.Border{{Type: "bottom", Color: "CDE4DB", Style: 1}}},
		"section":     {Font: base(12, "0F766E", true), Alignment: &excelize.Alignment{Vertical: "center"}},
		"selector":    {Font: base(12, "0F766E", true), Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"CCFBF1"}}, Alignment: &excelize.Alignment{Vertical: "center"}},
		"kpiLabel":    {Font: base(10, "64748B", false), Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F0FAF7"}}, Alignment: &excelize.Alignment{Vertical: "center", Indent: 1}},
		"kpi":         {Font: base(25, "0F766E", true), NumFmt: 3, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F0FAF7"}}, Alignment: &excelize.Alignment{Vertical: "center", Indent: 1}},
		"kpiMoney":    {Font: base(23, "0F766E", true), CustomNumFmt: usageStringPtr(`"$"#,##0.00######`), Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F0FAF7"}}, Alignment: &excelize.Alignment{Vertical: "center", Indent: 1}},
	}
	for name, style := range formats {
		id, err := b.f.NewStyle(style)
		b.check(err)
		b.styles[name] = id
	}
}

func usageStringPtr(value string) *string { return &value }

func usageDate(value string) time.Time { day, _ := time.Parse("2006-01-02", value); return day }

func usageExcelRange(sheet, column string, end int) string {
	return fmt.Sprintf("'%s'!$%s$%d:$%s$%d", sheet, column, usageDataRow, column, max(usageDataRow, end))
}

func (b *usageExcelBuilder) addRawData(report *GroupUsageReport) {
	b.header(usageUsers, []string{"用户ID", "用户名", "邮箱", "用户标签", "请求数", "输入Token", "输出Token", "缓存读取Token", "缓存创建Token", "总Token", "实际消费（USD）", "标准费用（USD）"})
	for i, u := range report.Users {
		row := usageDataRow + i
		for c, v := range []any{u.UserID, u.Username, u.UserEmail, u.Label(), u.Requests, u.InputTokens, u.OutputTokens, u.CacheRead, u.CacheWrite, u.TotalTokens, u.ActualCost, u.StandardCost} {
			style := "number"
			if c >= 1 && c <= 3 {
				style = "text"
			}
			if c >= 10 {
				style = "money"
			}
			b.put(usageUsers, row, c+1, v, style)
		}
		b.formula(usageUsers, row, 10, fmt.Sprintf("SUM(F%d:I%d)", row, row), u.TotalTokens, "number")
	}
	for _, sheet := range []string{usageDaily, usageModels} {
		heading := "日期（北京时间）"
		if sheet == usageModels {
			heading = "模型"
		}
		b.header(sheet, []string{"用户ID", heading, "请求数", "输入Token", "输出Token", "缓存读取Token", "缓存创建Token", "总Token", "实际消费（USD）", "标准费用（USD）"})
	}
	add := func(sheet string, row int, userID int64, key any, m UsageReportMetrics) {
		for c, v := range []any{userID, key, m.Requests, m.InputTokens, m.OutputTokens, m.CacheRead, m.CacheWrite, m.TotalTokens, m.ActualCost, m.StandardCost} {
			style := "number"
			if c == 1 {
				style = "text"
				if sheet == usageDaily {
					style = "date"
				}
			}
			if c >= 8 {
				style = "money"
			}
			b.put(sheet, row, c+1, v, style)
		}
		b.formula(sheet, row, 8, fmt.Sprintf("SUM(D%d:G%d)", row, row), m.TotalTokens, "number")
	}
	for i, d := range report.Daily {
		add(usageDaily, usageDataRow+i, d.UserID, usageDate(d.Date), d.UsageReportMetrics)
	}
	for i, m := range report.Models {
		add(usageModels, usageDataRow+i, m.UserID, m.Model, m.UsageReportMetrics)
	}
	for _, sheet := range []string{usageUsers, usageDaily, usageModels} {
		for col := 3; col <= 12; col++ {
			b.sheets[sheet].widths[col] = 19
		}
		b.sheets[sheet].widths[1] = 13
		b.sheets[sheet].widths[2] = 23
		if sheet == usageModels {
			b.sheets[sheet].widths[2] = 43
		}
		if sheet == usageUsers {
			b.sheets[sheet].widths[11] = 22
			b.sheets[sheet].widths[12] = 22
		} else {
			b.sheets[sheet].widths[9] = 22
			b.sheets[sheet].widths[10] = 22
		}
		if sheet == usageUsers {
			b.sheets[sheet].widths[3] = 30
			b.sheets[sheet].widths[4] = 36
		}
	}
}

type usageExcelModel struct {
	name    string
	metrics UsageReportMetrics
}

// 图表只展示前12名和前5型号加“其他”，完整用户及型号始终保留在明细表。
func (b *usageExcelBuilder) dashboard(report *GroupUsageReport, personal bool) {
	sheet := usageOverview
	if personal {
		sheet = usagePersonal
	}
	userEnd := usageDataRow + len(report.Users) - 1
	dailyEnd := usageDataRow + len(report.Daily) - 1
	modelEnd := usageDataRow + len(report.Models) - 1
	selectedID := int64(0)
	selectedMetrics := report.Summary
	selectedIndex := 0
	for i, u := range report.Users {
		if u.Requests > 0 {
			selectedIndex = i
			break
		}
	}
	if personal {
		if len(report.Users) > 0 {
			selectedID = report.Users[selectedIndex].UserID
			selectedMetrics = report.Users[selectedIndex].UsageReportMetrics
		}
		b.put(sheet, 4, 1, "选择用户", "muted")
		label := "本分组暂无用户"
		if len(report.Users) > 0 {
			label = report.Users[selectedIndex].Label()
		}
		b.merged(sheet, "B4", "H4", label, "selector")
		b.put(sheet, 4, 9, "用户ID", "muted")
		if len(report.Users) > 0 {
			b.formula(sheet, 4, 10, fmt.Sprintf("INDEX(%s,MATCH($B$4,%s,0))", usageExcelRange(usageUsers, "A", userEnd), usageExcelRange(usageUsers, "D", userEnd)), selectedID, "number")
		} else {
			b.put(sheet, 4, 10, 0, "number")
		}
		b.sheets[sheet].heights[4] = 30
	}
	titles := []string{"请求数", "总 Token", "活跃用户", "实际消费（USD）"}
	values := []any{report.Summary.Requests, report.Summary.TotalTokens, report.ActiveUsers, report.Summary.ActualCost}
	formulas := []string{fmt.Sprintf("SUM(%s)", usageExcelRange(usageUsers, "E", userEnd)), fmt.Sprintf("SUM(%s)", usageExcelRange(usageUsers, "J", userEnd)), fmt.Sprintf(`COUNTIFS(%s,">0")`, usageExcelRange(usageUsers, "E", userEnd)), fmt.Sprintf("SUM(%s)", usageExcelRange(usageUsers, "K", userEnd))}
	if personal {
		titles = []string{"请求数", "总 Token", "实际消费（USD）", "标准费用（USD）"}
		values = []any{selectedMetrics.Requests, selectedMetrics.TotalTokens, selectedMetrics.ActualCost, selectedMetrics.StandardCost}
		for i, col := range []string{"E", "J", "K", "L"} {
			formulas[i] = fmt.Sprintf("SUMIFS(%s,%s,$J$4)", usageExcelRange(usageUsers, col, userEnd), usageExcelRange(usageUsers, "A", userEnd))
		}
	}
	for i, title := range titles {
		col := 1 + i*4
		start, _ := excelize.CoordinatesToCellName(col, 6)
		end, _ := excelize.CoordinatesToCellName(col+3, 6)
		b.merged(sheet, start, end, title, "kpiLabel")
		start, _ = excelize.CoordinatesToCellName(col, 7)
		end, _ = excelize.CoordinatesToCellName(col+3, 9)
		style := "kpi"
		if (!personal && i == 3) || (personal && i >= 2) {
			style = "kpiMoney"
		}
		b.merged(sheet, start, end, "", style)
		b.formula(sheet, 7, col, formulas[i], values[i], style)
	}
	info := fmt.Sprintf("共%d位关联用户，统计区间内无调用的成员保留为0。所有费用均为本分组的美元账本金额。", len(report.Users))
	if personal {
		info = "切换上方用户后，指标、趋势和模型占比自动更新；仅包含该用户在本分组的用量。"
	}
	b.merged(sheet, "A10", "P10", info, "muted")
	section := "用户用量与费用明细"
	if personal {
		section = "个人用量明细（本分组）"
	}
	b.merged(sheet, "A31", "P31", section, "section")
	// 每日图表的数据区与导出区间共用相同的北京时间日历，首尾可以是部分日期。
	days := []string{}
	for day := usageDate(report.StartTime.Format("2006-01-02")); !day.After(usageDate(report.EndTime.Format("2006-01-02"))); day = day.AddDate(0, 0, 1) {
		days = append(days, day.Format("2006-01-02"))
	}
	helperStart := max(74, 38+len(days))
	daily := map[string]UsageReportMetrics{}
	for _, d := range report.Daily {
		if !personal || d.UserID == selectedID {
			m := daily[d.Date]
			m.add(d.UsageReportMetrics)
			daily[d.Date] = m
		}
	}
	b.merged(sheet, fmt.Sprintf("A%d", helperStart-3), fmt.Sprintf("P%d", helperStart-3), "图表数据（与明细工作表关联，可用于核对）", "section")
	for c, v := range []string{"日期", "日期标签", "输入", "输出", "缓存读取", "缓存创建", "请求", "实际消费"} {
		b.put(sheet, helperStart-1, c+1, v, "tableHeader")
	}
	for i, date := range days {
		row := helperStart + i
		m := daily[date]
		b.put(sheet, row, 1, usageDate(date), "date")
		b.put(sheet, row, 2, date[5:], "text")
		for j, rawCol := range []string{"D", "E", "F", "G", "C", "I"} {
			formula := fmt.Sprintf("SUMIFS(%s,%s,A%d", usageExcelRange(usageDaily, rawCol, dailyEnd), usageExcelRange(usageDaily, "B", dailyEnd), row)
			if personal {
				formula += fmt.Sprintf(",%s,$J$4", usageExcelRange(usageDaily, "A", dailyEnd))
			}
			formula += ")"
			cached := []any{m.InputTokens, m.OutputTokens, m.CacheRead, m.CacheWrite, m.Requests, m.ActualCost}[j]
			b.formula(sheet, row, j+3, formula, cached, "number")
		}
	}
	modelMap := map[string]UsageReportMetrics{}
	for _, m := range report.Models {
		if _, ok := modelMap[m.Model]; !ok {
			modelMap[m.Model] = UsageReportMetrics{}
		}
		if !personal || m.UserID == selectedID {
			v := modelMap[m.Model]
			v.add(m.UsageReportMetrics)
			modelMap[m.Model] = v
		}
	}
	names := []string{}
	for name := range modelMap {
		names = append(names, name)
	}
	sort.Strings(names)
	modelRank := make([]usageExcelModel, 0, len(names))
	for _, name := range names {
		modelRank = append(modelRank, usageExcelModel{name, modelMap[name]})
	}
	sort.SliceStable(modelRank, func(i, j int) bool { return modelRank[i].metrics.TotalTokens > modelRank[j].metrics.TotalTokens })
	ranks := map[string]int{}
	for i, m := range modelRank {
		ranks[m.name] = i + 1
	}
	for c, v := range []string{"模型", "总Token", "实际消费", "请求", "排序"} {
		b.put(sheet, helperStart-1, c+10, v, "tableHeader")
	}
	for i, name := range names {
		row := helperStart + i
		b.put(sheet, row, 10, name, "text")
		m := modelMap[name]
		for j, rawCol := range []string{"H", "I", "C"} {
			criterion := fmt.Sprintf(`"="&SUBSTITUTE(SUBSTITUTE(SUBSTITUTE(J%d,"~","~~"),"*","~*"),"?","~?")`, row)
			formula := fmt.Sprintf("SUMIFS(%s,%s,%s", usageExcelRange(usageModels, rawCol, modelEnd), usageExcelRange(usageModels, "B", modelEnd), criterion)
			if personal {
				formula += fmt.Sprintf(",%s,$J$4", usageExcelRange(usageModels, "A", modelEnd))
			}
			formula += ")"
			b.formula(sheet, row, j+11, formula, []any{m.TotalTokens, m.ActualCost, m.Requests}[j], "number")
		}
		b.formula(sheet, row, 14, fmt.Sprintf(`COUNTIFS($K$%d:$K$%d,">"&K%d)+COUNTIFS($K$%d:K%d,K%d)`, helperStart, helperStart+len(names)-1, row, helperStart, row, row), ranks[name], "number")
	}
	// 两套读者视图各有自己的动态模型排名，个人选择不会引用另一张表的已完成结果。
	donutStart := helperStart + 4 + len(names)
	slices := min(5, len(modelRank))
	for i := 0; i < slices; i++ {
		row := donutStart + i
		item := modelRank[i]
		b.formula(sheet, row, 10, fmt.Sprintf("INDEX($J$%d:$J$%d,MATCH(%d,$N$%d:$N$%d,0))", helperStart, helperStart+len(names)-1, i+1, helperStart, helperStart+len(names)-1), item.name, "text")
		b.formula(sheet, row, 11, fmt.Sprintf("LARGE($K$%d:$K$%d,%d)", helperStart, helperStart+len(names)-1, i+1), item.metrics.TotalTokens, "number")
	}
	if len(modelRank) > slices {
		var rest int64
		for _, m := range modelRank[slices:] {
			rest += m.metrics.TotalTokens
		}
		row := donutStart + slices
		b.put(sheet, row, 10, "其他型号", "text")
		b.formula(sheet, row, 11, fmt.Sprintf("SUM(K%d:K%d)-SUM(K%d:K%d)", helperStart, helperStart+len(names)-1, donutStart, row-1), rest, "number")
		slices++
	}
	if personal {
		b.headerAt(sheet, 33, []string{"日期", "请求数", "输入Token", "输出Token", "缓存读取", "缓存创建", "总Token", "实际消费（USD）"})
		for i, date := range days {
			row := 34 + i
			source := helperStart + i
			m := daily[date]
			b.put(sheet, row, 1, usageDate(date), "date")
			for j, col := range []string{"G", "C", "D", "E", "F"} {
				b.formula(sheet, row, j+2, fmt.Sprintf("%s%d", col, source), []any{m.Requests, m.InputTokens, m.OutputTokens, m.CacheRead, m.CacheWrite}[j], "number")
			}
			b.formula(sheet, row, 7, fmt.Sprintf("SUM(C%d:F%d)", row, row), m.TotalTokens, "number")
			b.formula(sheet, row, 8, fmt.Sprintf("H%d", source), m.ActualCost, "money")
		}
	} else {
		b.headerAt(sheet, 33, []string{"用户ID", "用户名", "请求数", "总Token", "实际消费（USD）", "标准费用（USD）"})
		for i, u := range report.Users {
			if i >= 30 {
				break
			}
			row := 34 + i
			source := usageDataRow + i
			b.put(sheet, row, 1, u.UserID, "number")
			b.put(sheet, row, 2, u.Username, "text")
			for j, col := range []string{"E", "J", "K", "L"} {
				style := "number"
				if j >= 2 {
					style = "money"
				}
				b.formula(sheet, row, j+3, fmt.Sprintf("'%s'!%s%d", usageUsers, col, source), []any{u.Requests, u.TotalTokens, u.ActualCost, u.StandardCost}[j], style)
			}
		}
		b.merged(sheet, "A65", "P66", "完整用户与模型记录均保存在明细工作表。上方用量排行展示导出时Token用量前12名。", "muted")
	}
	ranking := append([]UsageReportUser(nil), report.Users...)
	sort.SliceStable(ranking, func(i, j int) bool { return ranking[i].TotalTokens > ranking[j].TotalTokens })
	rankStart := helperStart + 4 + len(days)
	for i, u := range ranking {
		if i >= 12 {
			break
		}
		row := rankStart + i
		b.put(sheet, row, 1, u.Label(), "text")
		b.formula(sheet, row, 2, fmt.Sprintf("SUMIFS(%s,%s,%d)", usageExcelRange(usageUsers, "J", userEnd), usageExcelRange(usageUsers, "A", userEnd), u.UserID), u.TotalTokens, "number")
	}
	// 图表在流式数据写入后添加，这里记录各视图的绘制范围。
	b.chartsForDashboard(sheet, helperStart, len(days), len(ranking), rankStart, donutStart, slices, personal, report.Summary.TotalTokens)
}

func (b *usageExcelBuilder) headerAt(sheet string, row int, columns []string) {
	for c, title := range columns {
		b.put(sheet, row, c+1, title, "tableHeader")
	}
	b.sheets[sheet].heights[row] = 28
}

type usageExcelChart struct {
	sheet, cell string
	chart       *excelize.Chart
}

// 生成阶段暂存图表配置，数据写入完成后统一绑定到原生单元格范围。
var usageChartColors = []string{"3B82F6", "14B8A6", "8B5CF6", "F59E0B", "EC4899", "06B6D4"}

func usageChartTitle(value string) excelize.ChartTitle {
	return excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: value, Font: &excelize.Font{Family: "Microsoft YaHei", Size: 12, Bold: true, Color: "334155"}}}}
}

// 图表配置随builder保存，不使用包级可变状态，多个用户可以同时导出。
func (b *usageExcelBuilder) chartsForDashboard(sheet string, dailyStart, days, users, rankStart, donutStart, slices int, personal bool, tokens int64) {
	if b.pending == nil {
		b.pending = []usageExcelChart{}
	}
	chart := &excelize.Chart{Type: excelize.Line, Dimension: excelize.ChartDimension{Width: 710, Height: 320}, Title: usageChartTitle("每日 Token 使用趋势"), Legend: excelize.ChartLegend{Position: "bottom", Font: &excelize.Font{Family: "Microsoft YaHei", Size: 9}}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFFFFF"}}, YAxis: excelize.ChartAxis{MajorGridLines: true, Minimum: usageFloatPtr(0), NumFmt: excelize.ChartNumFmt{CustomNumFmt: `[>=1000000]0.0,,"M";[>=1000]0.0,"K";0`}, Font: excelize.Font{Size: 9, Color: "64748B"}}, XAxis: excelize.ChartAxis{Font: excelize.Font{Size: 9, Color: "64748B"}}}
	for i, col := range []string{"C", "D", "E", "F"} {
		chart.Series = append(chart.Series, excelize.ChartSeries{Name: fmt.Sprintf("'%s'!$%s$%d", sheet, col, dailyStart-1), Categories: fmt.Sprintf("'%s'!$B$%d:$B$%d", sheet, dailyStart, dailyStart+days-1), Values: fmt.Sprintf("'%s'!$%s$%d:$%s$%d", sheet, col, dailyStart, col, dailyStart+days-1), Line: excelize.LineOptions{Width: 2.5, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{usageChartColors[i]}}}, Marker: excelize.ChartMarker{Symbol: "circle", Size: 4}})
	}
	b.pending = append(b.pending, usageExcelChart{sheet, "A12", chart})
	if slices > 0 && tokens > 0 {
		points := []excelize.ChartDataPoint{}
		for i := 0; i < slices; i++ {
			points = append(points, excelize.ChartDataPoint{Index: i, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{usageChartColors[i%len(usageChartColors)]}}})
		}
		pie := &excelize.Chart{Type: excelize.Doughnut, HoleSize: 70, Dimension: excelize.ChartDimension{Width: 510, Height: 320}, Title: usageChartTitle("模型使用占比（总 Token）"), Legend: excelize.ChartLegend{Position: "bottom", Font: &excelize.Font{Family: "Microsoft YaHei", Size: 8}}, Series: []excelize.ChartSeries{{Name: "总 Token", Categories: fmt.Sprintf("'%s'!$J$%d:$J$%d", sheet, donutStart, donutStart+slices-1), Values: fmt.Sprintf("'%s'!$K$%d:$K$%d", sheet, donutStart, donutStart+slices-1), DataPoint: points}}, PlotArea: excelize.ChartPlotArea{ShowPercent: true, NumFmt: excelize.ChartNumFmt{CustomNumFmt: "0%"}}}
		b.pending = append(b.pending, usageExcelChart{sheet, "J12", pie})
	} else {
		b.merged(sheet, "J17", "P20", "统计区间内暂无模型Token用量", "muted")
	}
	if !personal && users > 0 {
		bar := &excelize.Chart{Type: excelize.Bar, Dimension: excelize.ChartDimension{Width: 620, Height: 320}, Title: usageChartTitle("用户 Token 用量（前12名）"), Legend: excelize.ChartLegend{Position: "none"}, Series: []excelize.ChartSeries{{Name: "总 Token", Categories: fmt.Sprintf("'%s'!$A$%d:$A$%d", sheet, rankStart, rankStart+min(users, 12)-1), Values: fmt.Sprintf("'%s'!$B$%d:$B$%d", sheet, rankStart, rankStart+min(users, 12)-1), Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"14B8A6"}}}}, YAxis: excelize.ChartAxis{ReverseOrder: true, Font: excelize.Font{Family: "Microsoft YaHei", Size: 9}}, XAxis: excelize.ChartAxis{Minimum: usageFloatPtr(0), MajorGridLines: true, NumFmt: excelize.ChartNumFmt{CustomNumFmt: `[>=1000000]0.0,,"M";[>=1000]0.0,"K";0`}}}
		b.pending = append(b.pending, usageExcelChart{sheet, "I33", bar})
	}
}

func usageFloatPtr(v float64) *float64 { return &v }

func (b *usageExcelBuilder) flush(name string) error {
	s := b.sheets[name]
	stream, err := b.f.NewStreamWriter(name)
	if err != nil {
		return err
	}
	for c := 1; c <= 16; c++ {
		width := 13.0
		if w, ok := s.widths[c]; ok {
			width = w
		}
		if err = stream.SetColWidth(c, c, width); err != nil {
			return err
		}
	}
	if err = stream.SetPanes(&excelize.Panes{Freeze: true, YSplit: 6, TopLeftCell: "A7", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	rowIDs := []int{}
	for row := range s.rows {
		rowIDs = append(rowIDs, row)
	}
	sort.Ints(rowIDs)
	for _, row := range rowIDs {
		maxCol := 0
		for col := range s.rows[row] {
			maxCol = max(maxCol, col)
		}
		values := make([]interface{}, maxCol)
		for col, cell := range s.rows[row] {
			values[col-1] = cell
		}
		cell, _ := excelize.CoordinatesToCellName(1, row)
		height := 23.0
		if h, ok := s.heights[row]; ok {
			height = h
		}
		if err = stream.SetRow(cell, values, excelize.RowOpts{Height: height}); err != nil {
			return err
		}
	}
	for _, merge := range s.merges {
		if err = stream.MergeCell(merge[0], merge[1]); err != nil {
			return err
		}
	}
	if err = stream.Flush(); err != nil {
		return err
	}
	return nil
}

func buildGroupUsageXLSX(report *GroupUsageReport) ([]byte, error) {
	b := &usageExcelBuilder{f: excelize.NewFile(), sheets: map[string]*usageExcelSheet{}, styles: map[string]int{}}
	defer func() { b.f.Close() }()
	names := []string{usageOverview, usagePersonal, usageUsers, usageDaily, usageModels}
	b.check(b.f.SetSheetName("Sheet1", usageOverview))
	for i, name := range names {
		if i > 0 {
			_, err := b.f.NewSheet(name)
			b.check(err)
		}
		b.sheets[name] = &usageExcelSheet{name: name, rows: map[int]map[int]excelize.Cell{}, heights: map[int]float64{}, widths: map[int]float64{}}
	}
	b.createStyles()
	period := fmt.Sprintf("%s（#%d）   %s 至 %s（北京时间）   导出于 %s", report.GroupName, report.GroupID, report.StartTime.Format("2006-01-02 15:04"), report.EndTime.Format("2006-01-02 15:04"), report.GeneratedAt.Format("2006-01-02 15:04"))
	for _, name := range names {
		b.title(name, "幻方有序 · "+name, period)
	}
	b.addRawData(report)
	b.dashboard(report, false)
	b.dashboard(report, true)
	if b.err != nil {
		return nil, b.err
	}
	for _, name := range names {
		if err := b.flush(name); err != nil {
			return nil, err
		}
	}
	// 流式数据先完整落盘，再通过普通模式添加绘图和选择器元数据。
	// 两种写入模式不在同一File中混用，避免导出时流缓存覆盖后加的验证或绘图节点。
	stage, err := b.f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	finished, err := excelize.OpenReader(bytes.NewReader(stage.Bytes()))
	if err != nil {
		return nil, err
	}
	b.f.Close()
	b.f = finished
	show := false
	for _, name := range names {
		view := &excelize.ViewOptions{ShowGridLines: &show}
		if name == usageOverview || name == usagePersonal {
			view.ZoomScale = usageFloatPtr(80)
		}
		b.check(b.f.SetSheetView(name, 0, view))
	}
	if len(report.Users) > 0 {
		b.check(b.f.SetDefinedName(&excelize.DefinedName{Name: "GroupReportUsers", RefersTo: usageExcelRange(usageUsers, "D", usageDataRow+len(report.Users)-1)}))
		validation := excelize.NewDataValidation(false)
		validation.Sqref = "B4"
		validation.Type = "list"
		validation.Formula1 = "GroupReportUsers"
		validation.ShowErrorMessage = true
		validation.SetError(excelize.DataValidationErrorStyleStop, "请选择分组成员", "只能选择此报表中的用户")
		b.check(b.f.AddDataValidation(usagePersonal, validation))
	}
	for _, chart := range b.pending {
		b.check(b.f.AddChart(chart.sheet, chart.cell, chart.chart))
	}
	mode := "auto"
	yes := true
	b.check(b.f.SetCalcProps(&excelize.CalcPropsOptions{CalcMode: &mode, FullCalcOnLoad: &yes, ForceFullCalc: &yes, CalcOnSave: &yes}))
	for _, info := range []struct {
		name       string
		rows, cols int
	}{{usageUsers, len(report.Users), 12}, {usageDaily, len(report.Daily), 10}, {usageModels, len(report.Models), 10}} {
		last, _ := excelize.CoordinatesToCellName(info.cols, 6+max(1, info.rows))
		b.check(b.f.AutoFilter(info.name, "A6:"+last, []excelize.AutoFilterOptions{}))
		b.check(b.f.SetPanes(info.name, &excelize.Panes{Freeze: true, YSplit: 6, XSplit: 1, TopLeftCell: "B7", ActivePane: "bottomRight"}))
	}
	if b.err != nil {
		return nil, b.err
	}
	buffer, err := b.f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
