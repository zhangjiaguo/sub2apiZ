package modeltrace

import (
	"math"
	"testing"
)

func TestParseNumbers(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []int
	}{
		{"简单空格分隔", "1 2 3 4 5", []int{1, 2, 3, 4, 5}},
		{"逗号分隔", "17, 42, 355, 1", []int{17, 42, 355, 1}},
		{"字母断游程取最长", "1 2 abc 3 4 5", []int{3, 4, 5}},
		{"说明文字在前后", "好的，这是数字：10 20 30 40 50 60 70 80，请查收。", []int{10, 20, 30, 40, 50, 60, 70, 80}},
		{"中文顿号分隔不断", "1、2、3、4、5", []int{1, 2, 3, 4, 5}},
		{"溢出数字跳过不断游程", "9999999999999999999999 1 2", []int{1, 2}},
		{"越界值跳过", "400 356 100 200", []int{100, 200}},
		{"并列取第一个", "1 2 3 zz 4 5 6", []int{1, 2, 3}},
		{"无数字", "no numbers here", []int{}},
	}
	for _, c := range cases {
		got := ParseNumbers(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: got %v want %v", c.name, got, c.want)
			}
		}
	}
}

func TestParseNumbersNumberedLines(t *testing.T) {
	// "1. 5\n2. 12\n3. 300"：数字 1 与 5 之间的分隔是 ". "（无字母）→ 同一游程
	// [1 5]；5 与 2 之间是 "\n"（无字母）→ 继续；2 与 12 之间 ". "；12 与 3 ". "；
	// 3 与 300 ". " → 整体是一个游程 [1 5 2 12 3 300]。
	got := ParseNumbers("1. 5\n2. 12\n3. 300")
	want := []int{1, 5, 2, 12, 3, 300}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestValidAnswerThreshold(t *testing.T) {
	cases := []struct {
		expected, want int
	}{
		{0, 80}, {100, 80}, {145, 80}, {146, 81}, // ceil(0.55*146)=81
		{292, 161}, {300, 165}, {332, 183}, {-5, 80},
	}
	for _, c := range cases {
		if got := ValidAnswerThreshold(c.expected); got != c.want {
			t.Fatalf("ValidAnswerThreshold(%d)=%d want %d", c.expected, got, c.want)
		}
	}
}

func TestNumberHistogram(t *testing.T) {
	h := NumberHistogram([]int{1, 1, 355, 100, 0, 400})
	if len(h) != MaxValue {
		t.Fatalf("len=%d want %d", len(h), MaxValue)
	}
	if h[0] != 2 || h[354] != 1 || h[99] != 1 {
		t.Fatalf("histogram wrong: h[0]=%v h[354]=%v h[99]=%v", h[0], h[354], h[99])
	}
	var sum float64
	for _, c := range h {
		sum += c
	}
	if sum != 4 {
		t.Fatalf("sum=%v want 4", sum)
	}
}

func TestHellingerDistance(t *testing.T) {
	a := make([]float64, MaxValue)
	b := make([]float64, MaxValue)
	for i := range a {
		a[i] = 1
		b[i] = 1
	}
	if d := HellingerDistance(a, b); d > 1e-9 {
		t.Fatalf("identical histograms distance %v", d)
	}
	// 完全不相交（各半支撑）：α 平滑下不为 1，但应显著大于 0（>0.3）。
	for i := 0; i < MaxValue; i++ {
		if i < MaxValue/2 {
			a[i], b[i] = 1, 0
		} else {
			a[i], b[i] = 0, 1
		}
	}
	if d := HellingerDistance(a, b); d < 0.3 || d >= 1 {
		t.Fatalf("disjoint histograms distance %v want (0.3,1)", d)
	}
	// 长度不一致返回 1。
	if d := HellingerDistance(a, []float64{1}); d != 1 {
		t.Fatalf("mismatched dims distance %v", d)
	}
}

func TestSplitIntoFour(t *testing.T) {
	nums := make([]int, 300)
	for i := range nums {
		nums[i] = i + 1
	}
	q := SplitIntoFour(nums)
	if len(q[0]) != 75 || len(q[1]) != 75 || len(q[2]) != 75 || len(q[3]) != 75 {
		t.Fatalf("quarters %d %d %d %d", len(q[0]), len(q[1]), len(q[2]), len(q[3]))
	}
	nums = make([]int, 302)
	q = SplitIntoFour(nums)
	total := len(q[0]) + len(q[1]) + len(q[2]) + len(q[3])
	if total != 302 {
		t.Fatalf("total=%d want 302", total)
	}
	// 空序列。
	q = SplitIntoFour(nil)
	for i, seg := range q {
		if len(seg) != 0 {
			t.Fatalf("empty quarter %d len %d", i, len(seg))
		}
	}
}

func TestOrderedBlockFeatureDims(t *testing.T) {
	f := OrderedBlockFeature(nil)
	if len(f) != OrderedDims {
		t.Fatalf("empty feature len=%d want %d", len(f), OrderedDims)
	}
	nums := make([]int, 300)
	for i := range nums {
		nums[i] = 177 // 全部落在同一值：直方图单桶 + 零方差。
	}
	f = OrderedBlockFeature(nums)
	var histSum float64
	for i := 0; i < 64; i++ {
		histSum += f[i]
	}
	if histSum != 300 {
		t.Fatalf("quarter histogram sum=%v want 300", histSum)
	}
	if f[64] != 177 || f[67] != 177 {
		t.Fatalf("quarter means wrong: %v %v", f[64], f[67])
	}
	if f[72] != 0 { // 前后半均值差为 0
		t.Fatalf("trend dim=%v want 0", f[72])
	}
	// 单值分布的桶：177 → bin (177-1)*16/355 = 7。
	for i := 0; i < 4; i++ {
		for b := 0; b < 16; b++ {
			want := 0.0
			if b == 7 {
				want = 75
			}
			if f[i*16+b] != want {
				t.Fatalf("quarter %d bin %d = %v want %v", i, b, f[i*16+b], want)
			}
		}
	}
}

func TestZNormalizeAndSoftmax(t *testing.T) {
	z := zNormalize([]float64{1, 2, 3})
	// 手工：mean=2 std=sqrt(2/3)
	want := []float64{-1.224744871391589, 0, 1.224744871391589}
	for i := range z {
		if math.Abs(z[i]-want[i]) > 1e-9 {
			t.Fatalf("z[%d]=%v want %v", i, z[i], want[i])
		}
	}
	// 常数向量 → 全 0（std floor）。
	z = zNormalize([]float64{5, 5, 5})
	for _, v := range z {
		if v != 0 {
			t.Fatalf("constant z=%v", z)
		}
	}
	p := softmax(1, []float64{0, 0})
	if len(p) != 2 || math.Abs(p[0]-0.5) > 1e-12 {
		t.Fatalf("uniform softmax %v", p)
	}
	p = softmax(1000, []float64{1, 0}) // 极端 β 也不溢出
	if p[0] < 0.99999 {
		t.Fatalf("sharp softmax %v", p)
	}
	var sum float64
	for _, v := range p {
		sum += v
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("softmax sum %v", sum)
	}
}
