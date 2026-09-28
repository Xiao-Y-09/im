// flood 建立 N 个连接（同一个群），报告连接前后的 goroutine 数；-n > 0 时每个连接尽快狂发 n 条，
// 统计收到的 error 码和服务端 /debug/vars 的变化。用于 Phase 2 的 goroutine 验收和 Phase 3 的背压验收。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func main() {
	api := flag.String("api", "http://localhost:8080", "HTTP base URL")
	nConns := flag.Int("conns", 50, "connections")
	perConn := flag.Int("n", 0, "messages per connection (0 = only count goroutines)")
	flag.Parse()

	suffix := fmt.Sprintf("%x", time.Now().UnixNano()%0xffffff)
	tokens := make([]string, *nConns)
	var wg sync.WaitGroup
	for i := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("fl%d_%s", i, suffix)
			call(http.MethodPost, *api+"/api/register", "", map[string]string{"username": name, "password": "secret123"}, nil)
			var lr struct {
				Token string `json:"token"`
			}
			call(http.MethodPost, *api+"/api/login", "", map[string]string{"username": name, "password": "secret123"}, &lr)
			tokens[i] = lr.Token
		}()
	}
	wg.Wait()

	var g struct {
		ID int64 `json:"id"`
	}
	call(http.MethodPost, *api+"/api/groups", tokens[0], map[string]string{"name": "flood-" + suffix}, &g)
	for _, t := range tokens[1:] {
		call(http.MethodPost, fmt.Sprintf("%s/api/groups/%d/join", *api, g.ID), t, nil, nil)
	}
	convID := fmt.Sprintf("g_%d", g.ID)

	before := goroutines(*api)
	varsBefore := vars(*api)
	wsBase := strings.Replace(*api, "http", "ws", 1) + "/ws?token="
	conns := make([]*websocket.Conn, *nConns)
	for i, t := range tokens {
		c, _, err := websocket.DefaultDialer.Dial(wsBase+url.QueryEscape(t), nil)
		if err != nil {
			log.Fatalf("dial %d: %v", i, err)
		}
		conns[i] = c
	}
	time.Sleep(time.Second)
	during := goroutines(*api)
	fmt.Printf("goroutines: before=%d with_%d_conns=%d (delta %d, %.2f per conn)\n",
		before, *nConns, during, during-before, float64(during-before)/float64(*nConns))

	var received, busy, otherErr atomic.Int64
	codes := sync.Map{}
	var readers sync.WaitGroup
	for _, c := range conns {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				_, data, err := c.ReadMessage()
				if err != nil {
					return
				}
				var env struct {
					Type    string `json:"type"`
					Payload struct {
						Code string `json:"code"`
					} `json:"payload"`
				}
				if json.Unmarshal(data, &env) != nil {
					continue
				}
				switch env.Type {
				case "chat":
					received.Add(1)
				case "error":
					if env.Payload.Code == "busy" {
						busy.Add(1)
					} else {
						otherErr.Add(1)
						codes.Store(env.Payload.Code, true)
					}
				}
			}
		}()
	}

	if *perConn > 0 {
		start := time.Now()
		var senders sync.WaitGroup
		for _, c := range conns {
			senders.Add(1)
			go func() {
				defer senders.Done()
				for k := 0; k < *perConn; k++ {
					b, _ := json.Marshal(map[string]any{
						"type": "chat", "conv_id": convID, "client_msg_id": uuid.NewString(),
						"payload": map[string]string{"text": "flood"},
					})
					if c.WriteMessage(websocket.TextMessage, b) != nil {
						return
					}
				}
			}()
		}
		senders.Wait()
		time.Sleep(3 * time.Second)
		sent := *nConns * *perConn
		fmt.Printf("flood: sent=%d in %s, busy errors=%d, other errors=%d, chat deliveries received=%d\n",
			sent, time.Since(start).Round(time.Millisecond), busy.Load(), otherErr.Load(), received.Load())
		codes.Range(func(k, _ any) bool { fmt.Printf("  other error code: %v\n", k); return true })
	}

	for _, c := range conns {
		c.Close()
	}
	readers.Wait()
	time.Sleep(2 * time.Second)
	after := goroutines(*api)
	varsAfter := vars(*api)
	fmt.Printf("goroutines after close=%d (baseline %d)\n", after, before)
	for _, k := range []string{"published", "busy_rejected", "delivered", "slow_kicked", "persisted", "persist_dropped"} {
		fmt.Printf("  %s: %d → %d (+%d)\n", k, varsBefore[k], varsAfter[k], varsAfter[k]-varsBefore[k])
	}
	fmt.Printf("  heap_alloc_mb: %.1f → %.1f\n", float64(varsBefore["heap"])/(1<<20), float64(varsAfter["heap"])/(1<<20))
}

func goroutines(api string) int64 {
	var r struct {
		N int64 `json:"n"`
	}
	call(http.MethodGet, api+"/debug/goroutines", "", nil, &r)
	return r.N
}

func vars(api string) map[string]int64 {
	var raw map[string]json.RawMessage
	call(http.MethodGet, api+"/debug/vars", "", nil, &raw)
	out := map[string]int64{}
	for k, v := range raw {
		var n int64
		if json.Unmarshal(v, &n) == nil {
			out[k] = n
		}
	}
	var ms struct {
		HeapAlloc int64 `json:"HeapAlloc"`
	}
	_ = json.Unmarshal(raw["memstats"], &ms)
	out["heap"] = ms.HeapAlloc
	return out
}

func call(method, u, token string, in, out any) {
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			log.Fatalf("%s %s: %d %s", method, u, resp.StatusCode, body)
		}
	}
}
