package service

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"html/template"
)

//go:embed user_message_usage_report.html
var groupUsageHTMLTemplate string

// JSON保留encoding/json对小于号等字符的转义，用户输入不能结束script元素。
// 模板内所有姓名、邮箱与模型标签也只通过textContent写入，不把数据当作HTML执行。
func buildGroupUsageHTML(report *GroupUsageReport) ([]byte, error) {
	data, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	view := struct {
		Group string
		Data  template.JS
	}{Group: report.GroupName, Data: template.JS(data)} // 数据仅来自安全JSON编码器。
	var out bytes.Buffer
	t, err := template.New("group-usage").Parse(groupUsageHTMLTemplate)
	if err != nil {
		return nil, err
	}
	if err = t.Execute(&out, view); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
