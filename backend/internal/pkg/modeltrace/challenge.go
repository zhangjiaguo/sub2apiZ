// Package modeltrace 自建 ModelTrace 式数字分布指纹：用「生成 K 个 1..355
// 整数」的挑战探测模型，对回答里的数字分布做指纹比对，判定上游实际服务的
// 模型与请求模型是否一致（降智/换模检测）。
//
// 原理：各模型对「随机整数」的生成有稳定的个体偏差（数字偏好、低位聚集、
// 序列走势等），跨模型可区分。用自有官号对每个候选模型采集样本构建指纹库，
// 检测时把账号×模型的探测回答与库比对即可——上游真服务的模型与库中哪个
// 质心最近，verdict 就是谁；请求 A 却贴 B 的质心 = 被换模。
//
// 移植自 codex-inspector 计划 Task 11 的算法规格（上游 ModelTrace 的
// unified_bank.json 不公开，指纹库改为自建；跨模型区分性使自建库有效）。
package modeltrace

import (
	crand "crypto/rand"
	"fmt"
	"math/big"
)

// Challenge 一次生成挑战：期望个数 + 完整提示语。
type Challenge struct {
	ExpectedCount int    // 期望模型输出的整数个数
	Prompt        string // 完整提示语
}

// 挑战参数：期望个数从 [292,332] 共 41 个值里不重复采样（计划 Task 11 Step 5）。
const (
	challengeMinCount = 292
	challengeMaxCount = 332
)

// 挑战语料：开头/动作/分隔/收尾随机组合。提示语本身会轻微影响分布，库与
// 检测共用同一生成器与语料，语料多样性避免固定提示语本身成为指纹。
var (
	challengeOpenings = []string{
		"我在做一组随机数分布的统计实验。",
		"帮我准备一份随机性测试用的数据。",
		"下面是一个简单的采样任务。",
		"我需要一批随机数做校验。",
		"这是一次数字分布采样。",
	}
	challengeActions = []string{
		"请生成 %d 个 1 到 355（含端点）的整数",
		"请写出 %d 个介于 1 和 355 之间的整数（含 1 和 355）",
		"请给出 %d 个从 1 至 355 范围内取的整数",
	}
	challengeSeparators = []string{
		"，用逗号分隔，写成一行",
		"，用空格分隔，写成一行",
		"，逐个用顿号分隔，写成一行",
		"，用逗号加空格分隔，写成一行",
	}
	challengeEndings = []string{
		"不要编号，不要解释，只输出数字本身。",
		"只输出数字，不要任何其他文字。",
		"直接给数字序列，无需说明。",
	}
)

// GenerateChallenges 生成 n 个挑战：期望个数从 292..332 不重复采样（n 超过
// 区间大小时截断到 41），语料组合随机。rnd 是 [0,n) 均匀随机整数源
// （生产用 CryptoRandIntn，测试可注入固定序列复现）。
func GenerateChallenges(n int, rnd func(int) int) []Challenge {
	pool := make([]int, 0, challengeMaxCount-challengeMinCount+1)
	for k := challengeMinCount; k <= challengeMaxCount; k++ {
		pool = append(pool, k)
	}
	// Fisher-Yates 部分洗牌：洗出的前 n 个即不重复采样。
	for i := 0; i < n && i < len(pool)-1; i++ {
		j := i + rnd(len(pool)-i)
		if j < i || j >= len(pool) {
			j = i
		}
		pool[i], pool[j] = pool[j], pool[i]
	}
	if n > len(pool) {
		n = len(pool)
	}
	if n < 0 {
		n = 0
	}
	out := make([]Challenge, 0, n)
	for i := 0; i < n; i++ {
		count := pool[i]
		action := fmt.Sprintf(challengeActions[rnd(len(challengeActions))%len(challengeActions)], count)
		out = append(out, Challenge{
			ExpectedCount: count,
			Prompt: challengeOpenings[rnd(len(challengeOpenings))%len(challengeOpenings)] +
				action +
				challengeSeparators[rnd(len(challengeSeparators))%len(challengeSeparators)] +
				"。" +
				challengeEndings[rnd(len(challengeEndings))%len(challengeEndings)],
		})
	}
	return out
}

// CryptoRandIntn crypto/rand 驱动的 [0,n) 均匀整数（n<=0 返回 0）。
func CryptoRandIntn(n int) int {
	if n <= 0 {
		return 0
	}
	v, err := crand.Int(crand.Reader, big.NewInt(int64(n)))
	if err != nil {
		// crypto/rand 失败极罕见（系统熵池枯竭）；退化为取模也保持可用。
		return int(fallbackSeed()) % n
	}
	return int(v.Int64())
}

// fallbackSeed crypto/rand 失败时的兜底熵。
func fallbackSeed() uint64 {
	var b [8]byte
	_, _ = crand.Read(b[:])
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	if v == 0 {
		v = 0x9e3779b97f4a7c15
	}
	return v
}
