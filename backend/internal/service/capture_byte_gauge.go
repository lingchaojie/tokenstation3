package service

import "sync/atomic"

// captureByteGauge 限制 pond 采集队列中在途 CaptureRecord 的总字节数，
// 防止大 body 突发把内存打爆（按字节而非条数限流）。max<=0 或 nil 表示不限。
//
// 生命周期：Submit 成功入队前 tryReserve；worker task 顶部 defer release，
// 覆盖 task 的所有退出路径（Write 成功/失败/drop、panic），单点释放不泄漏。
type captureByteGauge struct {
	max      int64
	inFlight atomic.Int64
}

// tryReserve 尝试预留 n 字节。成功返回 true；超预算则回滚并返回 false。
func (g *captureByteGauge) tryReserve(n int64) bool {
	if g == nil || g.max <= 0 {
		return true
	}
	if g.inFlight.Add(n) > g.max {
		g.inFlight.Add(-n)
		return false
	}
	return true
}

// release 归还 n 字节。nil/不限时为 no-op。
func (g *captureByteGauge) release(n int64) {
	if g == nil || g.max <= 0 {
		return
	}
	g.inFlight.Add(-n)
}

// recordBytes 估算一条 CaptureRecord 常驻内存的字节量（原始请求/响应 + 两头）。
func recordBytes(rec *CaptureRecord) int64 {
	if rec == nil {
		return 0
	}
	return int64(len(rec.RawRequest) + len(rec.RawResponse) + len(rec.RequestHeaders) + len(rec.ResponseHeaders))
}
