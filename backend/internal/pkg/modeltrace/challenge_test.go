package modeltrace

import (
	"strings"
	"testing"
)

// seqRnd 固定序列 rnd（越界取 0）：让测试可复现。
type seqRnd struct{ vals []int }

func (s *seqRnd) fn(n int) int {
	for _, v := range s.vals {
		if v >= 0 && v < n {
			s.vals = s.vals[1:]
			return v
		}
	}
	return 0
}

func TestGenerateChallengesCountRangeAndUniqueness(t *testing.T) {
	rnd := &seqRnd{vals: nil} // 全 0 = 恒取每个池首
	// 全 0 时洗牌不交换，挑战 292,293,...；语料恒取第 0 组合。
	got := GenerateChallenges(41, rnd.fn)
	if len(got) != 41 {
		t.Fatalf("len=%d want 41", len(got))
	}
	seen := map[int]bool{}
	for i, c := range got {
		if c.ExpectedCount < 292 || c.ExpectedCount > 332 {
			t.Fatalf("challenge %d count %d out of range", i, c.ExpectedCount)
		}
		if seen[c.ExpectedCount] {
			t.Fatalf("duplicated count %d", c.ExpectedCount)
		}
		seen[c.ExpectedCount] = true
		if !strings.Contains(c.Prompt, "355") {
			t.Fatalf("challenge %d prompt missing value range: %s", i, c.Prompt)
		}
		if !strings.Contains(c.Prompt, "整数") {
			t.Fatalf("challenge %d prompt missing 整数: %s", i, c.Prompt)
		}
	}
}

func TestGenerateChallengesCapsAtPoolSize(t *testing.T) {
	got := GenerateChallenges(100, CryptoRandIntn)
	if len(got) != 41 {
		t.Fatalf("len=%d want 41", len(got))
	}
	seen := map[int]bool{}
	for _, c := range got {
		seen[c.ExpectedCount] = true
	}
	if len(seen) != 41 {
		t.Fatalf("unique counts=%d want 41", len(seen))
	}
	if got, want := GenerateChallenges(0, CryptoRandIntn), 0; len(got) != want {
		t.Fatalf("len=%d want %d", len(got), want)
	}
}

func TestGenerateChallengesCryptoRandVaries(t *testing.T) {
	a := GenerateChallenges(10, CryptoRandIntn)
	b := GenerateChallenges(10, CryptoRandIntn)
	diff := false
	for i := range a {
		if a[i].Prompt != b[i].Prompt || a[i].ExpectedCount != b[i].ExpectedCount {
			diff = true
			break
		}
	}
	if !diff {
		t.Fatal("crypto rnd should vary challenge composition")
	}
}

func TestCryptoRandIntnRange(t *testing.T) {
	for i := 0; i < 200; i++ {
		n := []int{1, 2, 7, 41, 355}[i%5]
		if v := CryptoRandIntn(n); v < 0 || v >= n {
			t.Fatalf("CryptoRandIntn(%d)=%d out of range", n, v)
		}
	}
	if v := CryptoRandIntn(0); v != 0 {
		t.Fatalf("CryptoRandIntn(0)=%d want 0", v)
	}
}
