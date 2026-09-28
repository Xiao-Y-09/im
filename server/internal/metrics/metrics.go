package metrics

import "expvar"

// 通过 GET /debug/vars 暴露；压测时前后各抓一次求差值。
var (
	Published      = expvar.NewInt("published")
	BusyRejected   = expvar.NewInt("busy_rejected")
	Delivered      = expvar.NewInt("delivered")
	SlowKicked     = expvar.NewInt("slow_kicked")
	Persisted      = expvar.NewInt("persisted")
	PersistDropped = expvar.NewInt("persist_dropped")
)
