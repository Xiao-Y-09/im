package main

import "math"

// hist 是对数分桶的延迟直方图（微秒，每桶宽 2%）。
// 每个读 goroutine 各持一个，结束后合并，避免热路径上加锁或存下每一条延迟。
const (
	histGrowth  = 1.02
	histBuckets = 1200 // 1.02^1200 µs ≈ 2e10 µs，远超任何合理延迟
)

var logGrowth = math.Log(histGrowth)

type hist struct {
	counts [histBuckets]int64
	n      int64
	maxUS  int64
}

func (h *hist) add(us int64) {
	if us < 0 {
		us = 0 // 同机时钟下不应出现，出现就当 0
	}
	i := int(math.Log(float64(us)+1) / logGrowth)
	if i >= histBuckets {
		i = histBuckets - 1
	}
	h.counts[i]++
	h.n++
	if us > h.maxUS {
		h.maxUS = us
	}
}

func (h *hist) merge(o *hist) {
	for i := range h.counts {
		h.counts[i] += o.counts[i]
	}
	h.n += o.n
	if o.maxUS > h.maxUS {
		h.maxUS = o.maxUS
	}
}

// quantileMS 返回分位数所在桶的上界，误差不超过 2%。
func (h *hist) quantileMS(q float64) float64 {
	if h.n == 0 {
		return 0
	}
	rank := int64(math.Ceil(q * float64(h.n)))
	var acc int64
	for i, c := range h.counts {
		acc += c
		if acc >= rank {
			upper := math.Pow(histGrowth, float64(i+1)) - 1
			return math.Min(upper, float64(h.maxUS)) / 1000
		}
	}
	return float64(h.maxUS) / 1000
}
