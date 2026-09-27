package modeltrace

import (
	"math"
	"regexp"
	"strconv"
	"unicode"
)

// 指纹常量（计划 Task 11 规格）。
const (
	// MaxValue 数字值域上界（1..MaxValue）。
	MaxValue = 355
	// Alpha 直方图 hellinger 平滑系数（每桶加 α 再归一）。
	Alpha = 0.5
	// OrderedWeight 顺序块特征在融合分里的权重（bank robust.ordered_blocks.weight）。
	OrderedWeight = 0.25
	// OrderedDims 顺序块特征维度：4 段×16 值桶 + 10 摘要维。
	OrderedDims = 74
	// minValidCount / validRatioPercent 有效回答的最小数字个数规则：
	// max(80, ceil(0.55×expected))。
	minValidCount     = 80
	validRatioPercent = 55
	// varianceFloor 标准化方差下限（除零保护）。
	varianceFloor = 1e-12
	// zScoreFloor 跨模型 z 归一化的标准差下限。
	zScoreFloor = 1e-9
)

// digitRe 数字游程匹配。
var digitRe = regexp.MustCompile(`\d+`)

// containsUnicodeLetter 分隔文本里是否含 Unicode 字母（字母断游程——中文、
// 英文说明文字都算；纯标点/空白不断）。
func containsUnicodeLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// ParseNumbers 从模型输出提取数字游程：\d+ 逐个匹配，与上一个数字之间的
// 分隔文本含字母则断游程；只收 1..355；越界（如 Atoi 溢出）跳过但不断游程；
// 返回最长的游程（并列取第一个）。计划 Task 11 参考实现的移植。
func ParseNumbers(text string) []int {
	var runs [][]int
	var cur []int
	prevEnd := 0
	for _, m := range digitRe.FindAllStringIndex(text, -1) {
		sep := text[prevEnd:m[0]]
		n, err := strconv.Atoi(text[m[0]:m[1]])
		if len(cur) > 0 && containsUnicodeLetter(sep) {
			runs = append(runs, cur)
			cur = nil
		}
		if err == nil && n >= 1 && n <= MaxValue {
			cur = append(cur, n)
		}
		prevEnd = m[1]
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	best := []int{}
	for _, r := range runs {
		if len(r) > len(best) {
			best = r
		}
	}
	return best
}

// ValidAnswerThreshold 有效回答的最小数字个数：max(80, ceil(0.55×expected))。
func ValidAnswerThreshold(expectedCount int) int {
	if expectedCount <= 0 {
		return minValidCount
	}
	t := (expectedCount*validRatioPercent + 99) / 100
	if t < minValidCount {
		t = minValidCount
	}
	return t
}

// NumberHistogram 355 维频次直方图（越界值忽略）。
func NumberHistogram(nums []int) []float64 {
	h := make([]float64, MaxValue)
	for _, n := range nums {
		if n >= 1 && n <= MaxValue {
			h[n-1]++
		}
	}
	return h
}

// HellingerDistance α 平滑后的 hellinger 距离：p_i=(c_i+α)/(N+αK)，
// H = √(Σ(√p−√q)²)/√2 ∈ [0,1]。两个输入等长（=MaxValue）。
func HellingerDistance(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 1
	}
	k := float64(len(a))
	var na, nb float64
	for i := range a {
		na += a[i]
		nb += b[i]
	}
	var s float64
	for i := range a {
		p := (a[i] + Alpha) / (na + Alpha*k)
		q := (b[i] + Alpha) / (nb + Alpha*k)
		d := math.Sqrt(p) - math.Sqrt(q)
		s += d * d
	}
	return math.Sqrt(s) / math.Sqrt2
}

// SplitIntoFour 按输出顺序四等分（边界 q*L/4 向下取整，末段兜到结尾）。
func SplitIntoFour(nums []int) [4][]int {
	var out [4][]int
	l := len(nums)
	for q := 0; q < 4; q++ {
		lo := q * l / 4
		hi := l
		if q < 3 {
			hi = (q + 1) * l / 4
		}
		if lo > l {
			lo = l
		}
		if hi > l {
			hi = l
		}
		if lo > hi {
			lo = hi
		}
		out[q] = nums[lo:hi]
	}
	return out
}

// valueBin 值 → 16 桶之一（均匀分桶，端点收进末桶）。
func valueBin(v int) int {
	b := (v - 1) * 16 / MaxValue
	if b < 0 {
		b = 0
	}
	if b > 15 {
		b = 15
	}
	return b
}

// OrderedBlockFeature 74 维顺序块特征：
//   - 0..63   每段 16 值桶直方图（顺序信息：值随输出位置的分布）；
//   - 64..67  各段均值；68..71 各段总体标准差；
//   - 72      前半均值 − 后半均值（整体走势）；
//   - 73      四段均值的总体标准差（段间漂移）。
func OrderedBlockFeature(nums []int) []float64 {
	f := make([]float64, OrderedDims)
	if len(nums) == 0 {
		return f
	}
	quarters := SplitIntoFour(nums)
	for q, seg := range quarters {
		for _, v := range seg {
			f[q*16+valueBin(v)]++
		}
		mean, std := meanStdInt(seg)
		f[64+q] = mean
		f[68+q] = std
	}
	half := len(nums) / 2
	m1, _ := meanStdInt(nums[:half])
	m2, _ := meanStdInt(nums[half:])
	f[72] = m1 - m2
	_, qstd := meanStdFloat([]float64{f[64], f[65], f[66], f[67]})
	f[73] = qstd
	return f
}

// meanStdInt / meanStdFloat 均值与总体标准差（空序列返回 0,0）。
func meanStdInt(xs []int) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var s float64
	for _, x := range xs {
		s += float64(x)
	}
	m := s / float64(len(xs))
	var v float64
	for _, x := range xs {
		d := float64(x) - m
		v += d * d
	}
	return m, math.Sqrt(v / float64(len(xs)))
}

func meanStdFloat(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	m := s / float64(len(xs))
	var v float64
	for _, x := range xs {
		d := x - m
		v += d * d
	}
	return m, math.Sqrt(v / float64(len(xs)))
}

// standardizedEuclid 标准化欧氏距离：sqrt(Σ (x−c)²/(var+floor))。
func standardizedEuclid(x, centroid, variance []float64) float64 {
	if len(x) != len(centroid) || len(x) != len(variance) {
		return math.Inf(1)
	}
	var s float64
	for i := range x {
		d := x[i] - centroid[i]
		s += d * d / (variance[i] + varianceFloor)
	}
	return math.Sqrt(s)
}

// zNormalize 跨模型归一化：v[i] → (v[i]−mean)/max(std, floor)。就地返回新切片。
func zNormalize(v []float64) []float64 {
	if len(v) == 0 {
		return v
	}
	m, std := meanStdFloat(v)
	if std < zScoreFloor {
		std = zScoreFloor
	}
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = (x - m) / std
	}
	return out
}

// softmax β 缩放后的 softmax（数值稳定实现）。
func softmax(beta float64, v []float64) []float64 {
	out := make([]float64, len(v))
	if len(v) == 0 {
		return out
	}
	maxv := beta * v[0]
	for _, x := range v {
		if s := beta * x; s > maxv {
			maxv = s
		}
	}
	var sum float64
	for i, x := range v {
		e := math.Exp(beta*x - maxv)
		out[i] = e
		sum += e
	}
	if sum <= 0 {
		sum = 1
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}
