// order_check 验证跨实例的全局顺序：3 个用户分别连 8080、8081、8080，加入同一个群，
// 同时各发 N 条；每个客户端记录收到的 seq 列表，三份必须完全相同、严格递增、长度 3N。
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
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type envelope struct {
	Type        string          `json:"type"`
	ConvID      string          `json:"conv_id"`
	ClientMsgID string          `json:"client_msg_id"`
	Seq         int64           `json:"seq"`
	Payload     json.RawMessage `json:"payload"`
}

type user struct {
	name  string
	id    int64
	token string
	api   string
}

func main() {
	apisFlag := flag.String("apis", "http://localhost:8080,http://localhost:8081", "comma separated HTTP bases; user i connects to apis[i % len]")
	perUser := flag.Int("n", 300, "messages per user")
	nUsers := flag.Int("users", 3, "number of users")
	timeout := flag.Duration("timeout", 60*time.Second, "max wait for all messages")
	flag.Parse()

	apis := strings.Split(*apisFlag, ",")
	suffix := strconv.FormatInt(time.Now().UnixNano()%1e9, 36)

	users := make([]*user, *nUsers)
	for i := range users {
		u := &user{name: fmt.Sprintf("oc%d_%s", i, suffix), api: apis[i%len(apis)]}
		must(register(apis[0], u.name))
		u.token, u.id = mustLogin(u.api, u.name)
		users[i] = u
	}

	var g struct {
		ID int64 `json:"id"`
	}
	mustDo(http.MethodPost, apis[0]+"/api/groups", users[0].token, map[string]string{"name": "order-" + suffix}, &g)
	for _, u := range users[1:] {
		mustDo(http.MethodPost, fmt.Sprintf("%s/api/groups/%d/join", u.api, g.ID), u.token, nil, nil)
	}
	convID := fmt.Sprintf("g_%d", g.ID)
	total := *perUser * *nUsers
	log.Printf("group %s with %d users, each sends %d → expect %d per client", convID, *nUsers, *perUser, total)

	type result struct {
		seqs   []int64
		errors int
	}
	results := make([]*result, len(users))
	conns := make([]*websocket.Conn, len(users))
	var wg sync.WaitGroup
	for i, u := range users {
		wsURL := strings.Replace(u.api, "http", "ws", 1) + "/ws?token=" + url.QueryEscape(u.token)
		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			log.Fatalf("dial %s: %v", u.api, err)
		}
		conns[i] = c
		r := &result{}
		results[i] = r
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadline := time.Now().Add(*timeout)
			for len(r.seqs)+r.errors < total && time.Now().Before(deadline) {
				_ = c.SetReadDeadline(deadline)
				_, data, err := c.ReadMessage()
				if err != nil {
					return
				}
				var env envelope
				if json.Unmarshal(data, &env) != nil {
					continue
				}
				switch {
				case env.Type == "chat" && env.ConvID == convID:
					r.seqs = append(r.seqs, env.Seq)
				case env.Type == "error":
					r.errors++
					log.Printf("user %d got error: %s", i, env.Payload)
				}
			}
		}()
	}
	time.Sleep(500 * time.Millisecond)

	start := time.Now()
	var sendWG sync.WaitGroup
	for i, c := range conns {
		sendWG.Add(1)
		go func() {
			defer sendWG.Done()
			for k := 0; k < *perUser; k++ {
				p, _ := json.Marshal(map[string]string{"text": fmt.Sprintf("u%d-%d", i, k)})
				b, _ := json.Marshal(envelope{Type: "chat", ConvID: convID, ClientMsgID: uuid.NewString(), Payload: p})
				if err := c.WriteMessage(websocket.TextMessage, b); err != nil {
					log.Printf("user %d write: %v", i, err)
					return
				}
			}
		}()
	}
	sendWG.Wait()
	wg.Wait()
	elapsed := time.Since(start)
	for _, c := range conns {
		c.Close()
	}

	ok := true
	for i, r := range results {
		inc := strictlyIncreasing(r.seqs)
		log.Printf("user %d (%s): received %d, errors %d, strictly increasing=%v", i, users[i].api, len(r.seqs), r.errors, inc)
		if len(r.seqs) != total || !inc {
			ok = false
		}
	}
	for i := 1; i < len(results); i++ {
		if !equal(results[0].seqs, results[i].seqs) {
			log.Printf("user 0 and user %d saw different sequences", i)
			ok = false
		}
	}
	log.Printf("elapsed %s", elapsed.Round(time.Millisecond))
	if !ok {
		fmt.Println("ORDER CHECK FAILED")
		os.Exit(1)
	}
	fmt.Printf("ORDER CHECK PASSED: %d clients saw the same %d seqs in the same strictly increasing order\n", len(results), total)
}

func strictlyIncreasing(s []int64) bool {
	for i := 1; i < len(s); i++ {
		if s[i] <= s[i-1] {
			return false
		}
	}
	return true
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func register(api, name string) error {
	status, body, err := do(http.MethodPost, api+"/api/register", "", map[string]string{"username": name, "password": "secret123"})
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusConflict {
		return fmt.Errorf("register %s: %d %s", name, status, body)
	}
	return nil
}

func mustLogin(api, name string) (string, int64) {
	var lr struct {
		Token string `json:"token"`
		User  struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	mustDo(http.MethodPost, api+"/api/login", "", map[string]string{"username": name, "password": "secret123"}, &lr)
	return lr.Token, lr.User.ID
}

func mustDo(method, u, token string, in, out any) {
	status, body, err := do(method, u, token, in)
	if err != nil {
		log.Fatal(err)
	}
	if status/100 != 2 {
		log.Fatalf("%s %s: %d %s", method, u, status, body)
	}
	if out != nil {
		must(json.Unmarshal(body, out))
	}
}

func do(method, u, token string, in any) (int, []byte, error) {
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
