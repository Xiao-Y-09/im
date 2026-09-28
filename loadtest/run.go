package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"
)

// payload 凑到约 200 字节：{"t":"lt","sent_at":<19 位 ns>,"pad":"..."} 的固定部分是 49 字节
var pad = strings.Repeat("x", 200-49)

type conn struct {
	uid int64
	ws  *websocket.Conn
	wmu sync.Mutex // gorilla 不允许并发写；多个发送 worker 可能选中同一个连接

	h        hist // 只由本连接的读 goroutine 写
	received atomic.Int64
	busy     atomic.Int64
	otherErr atomic.Int64
	dropped  atomic.Bool
}

type liveGroup struct {
	id      int64
	members []*conn // 本次压测中在线（已连接）的成员
}

type serverSnap struct {
	Base       string           `json:"base"`
	Goroutines int64            `json:"goroutines"`
	SysMB      float64          `json:"sys_mb"`
	Vars       map[string]int64 `json:"vars"`
	Err        string           `json:"err,omitempty"`
}

var counterNames = []string{"published", "busy_rejected", "delivered", "slow_kicked", "persisted", "persist_dropped"}

type serverReport struct {
	Base             string           `json:"base"`
	GoroutinesBefore int64            `json:"goroutines_before"`
	GoroutinesDuring int64            `json:"goroutines_during"`
	GoroutinesAfter  int64            `json:"goroutines_after"`
	SysMBDuring      float64          `json:"sys_mb_during"`
	Delta            map[string]int64 `json:"delta"`
}

type result struct {
	Label       string         `json:"label"`
	Instances   int            `json:"instances"`
	ConnsWanted int            `json:"conns_wanted"`
	Conns       int            `json:"conns"`
	ConnFailed  int64          `json:"conn_failed"`
	ConnDropped int64          `json:"conn_dropped"`
	GroupSize   int            `json:"group_size"`
	TargetRate  float64        `json:"target_rate"`
	DurationSec float64        `json:"duration_sec"`
	Attempted   int64          `json:"attempted"`
	WriteErrors int64          `json:"write_errors"`
	BusyErrors  int64          `json:"busy_errors"`
	OtherErrors int64          `json:"other_errors"`
	ActualRate  float64        `json:"actual_rate"`
	Expected    int64          `json:"expected"`
	Received    int64          `json:"received"`
	Loss        float64        `json:"loss"`
	P50ms       float64        `json:"p50_ms"`
	P99ms       float64        `json:"p99_ms"`
	MaxMs       float64        `json:"max_ms"`
	Servers     []serverReport `json:"servers"`
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	urlsFlag := fs.String("urls", "ws://localhost:8080/ws", "comma separated WS URLs; connections are assigned round-robin")
	nConns := fs.Int("conns", 1000, "number of WebSocket connections (one user each)")
	target := fs.Float64("rate", 8000, "total send rate, msg/s")
	duration := fs.Duration("duration", 60*time.Second, "measured duration")
	warmup := fs.Duration("warmup", 10*time.Second, "warmup before measuring (not counted)")
	drain := fs.Duration("drain", 5*time.Second, "wait after sending stops before counting")
	fixturePath := fs.String("fixture", "loadtest/fixture.json", "fixture from 'seed'")
	senders := fs.Int("senders", 64, "sending worker goroutines")
	label := fs.String("label", "", "mode label for the results row, e.g. kafka / local")
	resultsMD := fs.String("results-md", "", "append a markdown row to this file")
	_ = fs.Parse(args)

	fx, err := loadFixture(*fixturePath)
	if err != nil {
		return err
	}
	if *nConns > len(fx.Users) {
		return fmt.Errorf("-conns %d > %d users in fixture; re-run seed with more users", *nConns, len(fx.Users))
	}
	urls := strings.Split(*urlsFlag, ",")
	bases := make([]string, len(urls))
	for i, u := range urls {
		if bases[i], err = httpBase(u); err != nil {
			return err
		}
	}

	before := snapAll(bases)

	// ---- 建立连接 ----
	log.Printf("logging in and connecting %d clients to %v ...", *nConns, urls)
	conns := make([]*conn, *nConns)
	var connFailed atomic.Int64
	var logged atomic.Int32
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	parallel(*nConns, 50, func(i int) {
		u := fx.Users[i]
		base, wsURL := bases[i%len(bases)], urls[i%len(urls)]
		tok, _, err := login(base, u.Username, fx.Password)
		if err == nil {
			var ws *websocket.Conn
			ws, _, err = dialer.Dial(wsURL+"?token="+url.QueryEscape(tok), nil)
			if err == nil {
				conns[i] = &conn{uid: u.ID, ws: ws}
				return
			}
		}
		connFailed.Add(1)
		if logged.Add(1) <= 5 {
			log.Printf("conn %d (%s) failed: %v", i, u.Username, err)
		}
	})
	byUID := map[int64]*conn{}
	var live []*conn
	for _, c := range conns {
		if c != nil {
			byUID[c.uid] = c
			live = append(live, c)
		}
	}
	log.Printf("connected %d, failed %d", len(live), connFailed.Load())
	if len(live) == 0 {
		return fmt.Errorf("no connections")
	}

	var groups []liveGroup
	for _, g := range fx.Groups {
		lg := liveGroup{id: g.ID}
		for _, m := range g.Members {
			if c := byUID[m]; c != nil {
				lg.members = append(lg.members, c)
			}
		}
		if len(lg.members) > 0 {
			groups = append(groups, lg)
		}
	}

	// 本轮专属前缀，区分预热/正式，也和别的轮次、别的客户端的消息隔开
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	runID = runID[len(runID)-6:]
	measuredPrefix, warmPrefix := "lt"+runID+"m", "lt"+runID+"w"

	var closing atomic.Bool
	var readers sync.WaitGroup
	for _, c := range live {
		readers.Add(1)
		go c.readLoop(measuredPrefix, &closing, &readers)
	}

	// ---- 发送 ----
	start := time.Now()
	measureStart := start.Add(*warmup).UnixNano()
	end := start.Add(*warmup + *duration)
	log.Printf("sending at %.0f msg/s: warmup %s, measure %s", *target, *warmup, *duration)

	burst := max(1, int(*target/50))
	lim := rate.NewLimiter(rate.Limit(*target), burst)
	ctx, cancel := context.WithDeadline(context.Background(), end)
	defer cancel()

	var attempted, expected, writeErrs, seq atomic.Int64
	var sendWG sync.WaitGroup
	for w := 0; w < *senders; w++ {
		sendWG.Add(1)
		go func() {
			defer sendWG.Done()
			rng := rand.New(rand.NewPCG(uint64(w), uint64(time.Now().UnixNano())))
			for {
				if lim.Wait(ctx) != nil {
					return
				}
				now := time.Now()
				g := groups[rng.IntN(len(groups))]
				c := g.members[rng.IntN(len(g.members))]
				measured := now.UnixNano() >= measureStart
				prefix := warmPrefix
				if measured {
					prefix = measuredPrefix
				}
				cmid := fmt.Sprintf("%s%0*d", prefix, 36-len(prefix), seq.Add(1))
				msg := fmt.Sprintf(`{"type":"chat","conv_id":"g_%d","client_msg_id":"%s","payload":{"t":"lt","sent_at":%d,"pad":"%s"}}`,
					g.id, cmid, now.UnixNano(), pad)

				c.wmu.Lock()
				_ = c.ws.SetWriteDeadline(now.Add(10 * time.Second))
				err := c.ws.WriteMessage(websocket.TextMessage, []byte(msg))
				c.wmu.Unlock()

				if measured {
					// 写失败也计入应收：发不出去同样是系统没把消息送到
					attempted.Add(1)
					expected.Add(int64(len(g.members)))
					if err != nil {
						writeErrs.Add(1)
					}
				}
			}
		}()
	}
	sendWG.Wait()
	log.Printf("sending done, draining %s ...", *drain)
	time.Sleep(*drain)

	during := snapAll(bases)

	closing.Store(true)
	for _, c := range live {
		c.wmu.Lock()
		_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		c.wmu.Unlock()
		c.ws.Close()
	}
	readers.Wait()
	time.Sleep(2 * time.Second) // 让服务端回收连接 goroutine
	after := snapAll(bases)

	// ---- 汇总 ----
	var total hist
	r := result{
		Label:       *label,
		Instances:   len(urls),
		ConnsWanted: *nConns,
		Conns:       len(live),
		ConnFailed:  connFailed.Load(),
		GroupSize:   fx.GroupSize,
		TargetRate:  *target,
		DurationSec: duration.Seconds(),
		Attempted:   attempted.Load(),
		WriteErrors: writeErrs.Load(),
		Expected:    expected.Load(),
	}
	for _, c := range live {
		total.merge(&c.h)
		r.Received += c.received.Load()
		r.BusyErrors += c.busy.Load()
		r.OtherErrors += c.otherErr.Load()
		if c.dropped.Load() {
			r.ConnDropped++
		}
	}
	ok := r.Attempted - r.WriteErrors - r.BusyErrors - r.OtherErrors
	r.ActualRate = float64(ok) / duration.Seconds()
	if r.Expected > 0 {
		r.Loss = 1 - float64(r.Received)/float64(r.Expected)
	}
	r.P50ms = total.quantileMS(0.50)
	r.P99ms = total.quantileMS(0.99)
	r.MaxMs = float64(total.maxUS) / 1000
	for i := range bases {
		sr := serverReport{
			Base:             bases[i],
			GoroutinesBefore: before[i].Goroutines,
			GoroutinesDuring: during[i].Goroutines,
			GoroutinesAfter:  after[i].Goroutines,
			SysMBDuring:      during[i].SysMB,
			Delta:            map[string]int64{},
		}
		for _, k := range counterNames {
			sr.Delta[k] = after[i].Vars[k] - before[i].Vars[k]
		}
		r.Servers = append(r.Servers, sr)
	}

	b, _ := json.Marshal(r)
	fmt.Println(string(b))
	printTable(&r)
	if *resultsMD != "" {
		if err := appendRow(*resultsMD, &r); err != nil {
			return err
		}
	}
	return nil
}

func (c *conn) readLoop(measuredPrefix string, closing *atomic.Bool, wg *sync.WaitGroup) {
	defer wg.Done()
	var env struct {
		Type        string          `json:"type"`
		ClientMsgID string          `json:"client_msg_id"`
		Payload     json.RawMessage `json:"payload"`
	}
	var chat struct {
		SentAt int64 `json:"sent_at"`
	}
	var errp struct {
		Code string `json:"code"`
	}
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			if !closing.Load() {
				c.dropped.Store(true)
			}
			return
		}
		now := time.Now().UnixNano()
		env.Type, env.ClientMsgID, env.Payload = "", "", nil
		if json.Unmarshal(data, &env) != nil || !strings.HasPrefix(env.ClientMsgID, measuredPrefix) {
			continue
		}
		switch env.Type {
		case "chat":
			chat.SentAt = 0
			if json.Unmarshal(env.Payload, &chat) == nil && chat.SentAt > 0 {
				c.received.Add(1)
				c.h.add((now - chat.SentAt) / 1000)
			}
		case "error":
			errp.Code = ""
			_ = json.Unmarshal(env.Payload, &errp)
			if errp.Code == "busy" {
				c.busy.Add(1)
			} else {
				c.otherErr.Add(1)
			}
		}
	}
}

func loadFixture(path string) (*fixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture (run 'seed' first): %w", err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func snapAll(bases []string) []serverSnap {
	out := make([]serverSnap, len(bases))
	for i, b := range bases {
		out[i] = snap(b)
	}
	return out
}

func snap(base string) serverSnap {
	s := serverSnap{Base: base, Vars: map[string]int64{}}
	var g struct {
		N int64 `json:"n"`
	}
	if _, err := doJSON(http.MethodGet, base+"/debug/goroutines", "", nil, &g); err != nil {
		s.Err = err.Error()
		return s
	}
	s.Goroutines = g.N

	var vars map[string]json.RawMessage
	if _, err := doJSON(http.MethodGet, base+"/debug/vars", "", nil, &vars); err != nil {
		s.Err = err.Error()
		return s
	}
	for _, k := range counterNames {
		var v int64
		_ = json.Unmarshal(vars[k], &v)
		s.Vars[k] = v
	}
	var ms struct {
		Sys uint64 `json:"Sys"`
	}
	if json.Unmarshal(vars["memstats"], &ms) == nil {
		s.SysMB = float64(ms.Sys) / (1 << 20)
	}
	return s
}

func printTable(r *result) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "label\t%s\n", r.Label)
	fmt.Fprintf(tw, "connections\t%d / %d (failed %d, dropped during run %d)\n", r.Conns, r.ConnsWanted, r.ConnFailed, r.ConnDropped)
	fmt.Fprintf(tw, "target rate\t%.0f msg/s\n", r.TargetRate)
	fmt.Fprintf(tw, "actual rate\t%.1f msg/s  (attempted %d, write err %d, busy %d, other err %d, over %.0fs)\n",
		r.ActualRate, r.Attempted, r.WriteErrors, r.BusyErrors, r.OtherErrors, r.DurationSec)
	fmt.Fprintf(tw, "expected / received\t%d / %d\n", r.Expected, r.Received)
	fmt.Fprintf(tw, "loss\t%.4f%%\n", r.Loss*100)
	fmt.Fprintf(tw, "latency p50 / p99 / max\t%.2f / %.2f / %.2f ms\n", r.P50ms, r.P99ms, r.MaxMs)
	for _, s := range r.Servers {
		fmt.Fprintf(tw, "server %s\tgoroutines %d → %d → %d, sys %.0f MB, delta %v\n",
			s.Base, s.GoroutinesBefore, s.GoroutinesDuring, s.GoroutinesAfter, s.SysMBDuring, s.Delta)
	}
	tw.Flush()
}

func appendRow(path string, r *result) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "| %s | %d | %d | %d | %.0f | %.0f | %.4f%% | %.1f ms | %d |\n",
		r.Label, r.Instances, r.Conns, r.GroupSize, r.TargetRate, r.ActualRate, r.Loss*100, r.P99ms, r.BusyErrors)
	return err
}
