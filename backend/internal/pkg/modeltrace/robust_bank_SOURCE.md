robust_bank.json 是上游 ModelTrace 项目的统一全局稳健数字指纹库
（16 模型 × 36 响应，12 采集环境），来源链：

- 原始项目：xqy2006/ModelTrace commit 55a2e4a55170423b484d701e9a82ab62b268c811
  原始路径 static/data/unified_bank.json（MIT，见 robust_bank_LICENSE）
- 经由 MACOS-DO/sub4api 仓库分发（backend/internal/service/modeltrace_unified_bank.json，
  与 backend/cmd/codex-ticket-hypothesis/modeltrace/unified_bank.json 两份拷贝逐字节一致）

本目录的评分引擎移植（robust.go）参考了同一来源的
static/fingerprint-core.mjs 与 sub4api 的 Go 移植 internal/service/codex_fingerprint.go。
挑战生成器（GenerateRobustChallenges）移植自 static/challenge-browser.js /
sub4api internal/service/modeltrace_challenge.go。
