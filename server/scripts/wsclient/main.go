// wsclient 是测试用的命令行 WebSocket 客户端：登录 → 连接 → 从 stdin 读命令发消息 → 打印收到的消息。
//
// stdin 命令：
//
//	/conv <conv_id>                      设置默认会话，之后直接输入文字就发到这里
//	/to <uid> <text>                     单聊
//	/g <gid> <text>                      群聊
//	/file <conv_id> <url> <name> <size> <mime>
//	/call <invite|accept|reject|offer|answer|ice|hangup> <uid> [payload-json]
//	/raw <envelope-json>                 原样发送
//	/sleep <ms>                          脚本里用来控制时序
//	/quit
//
// 收到的每条消息打印为一行 "<< {json}"，方便脚本 grep。
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
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

	"github.com/gorilla/websocket"
)

type envelope struct {
	Type        string          `json:"type"`
	ConvID      string          `json:"conv_id"`
	From        int64           `json:"from,omitempty"`
	To          int64           `json:"to,omitempty"`
	ClientMsgID string          `json:"client_msg_id"`
	Seq         int64           `json:"seq,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	TS          int64           `json:"ts,omitempty"`
}

var outMu sync.Mutex

func say(format string, args ...any) {
	outMu.Lock()
	defer outMu.Unlock()
	fmt.Printf(format+"\n", args...)
}

func main() {
	apiBase := flag.String("api", "http://localhost:8080", "HTTP base URL of the server")
	user := flag.String("user", "", "username")
	pass := flag.String("pass", "secret123", "password")
	register := flag.Bool("register", false, "register the user first (ignore 'already taken')")
	linger := flag.Duration("linger", 2*time.Second, "keep receiving this long after stdin EOF")
	flag.Parse()
	if *user == "" {
		log.Fatal("-user is required")
	}

	if *register {
		status, body, err := postJSON(*apiBase+"/api/register", map[string]string{"username": *user, "password": *pass})
		if err != nil {
			log.Fatal(err)
		}
		if status != http.StatusCreated && status != http.StatusConflict {
			log.Fatalf("register: %d %s", status, body)
		}
	}
	status, body, err := postJSON(*apiBase+"/api/login", map[string]string{"username": *user, "password": *pass})
	if err != nil {
		log.Fatal(err)
	}
	if status != http.StatusOK {
		log.Fatalf("login: %d %s", status, body)
	}
	var lr struct {
		Token string `json:"token"`
		User  struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &lr); err != nil {
		log.Fatal(err)
	}
	me := lr.User.ID
	say("me uid=%d username=%s", me, lr.User.Username)

	wsURL := strings.Replace(*apiBase, "http", "ws", 1) + "/ws?token=" + url.QueryEscape(lr.Token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	say("connected %s", strings.SplitN(wsURL, "?", 2)[0])

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				say("!! read: %v", err)
				return
			}
			say("<< %s", data)
		}
	}()

	send := func(env envelope) {
		if env.ClientMsgID == "" {
			env.ClientMsgID = newUUID()
		}
		if len(env.Payload) == 0 {
			env.Payload = json.RawMessage("{}")
		}
		b, _ := json.Marshal(env)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			say("!! write: %v", err)
			return
		}
		say(">> %s", b)
	}
	text := func(s string) json.RawMessage {
		b, _ := json.Marshal(map[string]string{"text": s})
		return b
	}

	defaultConv := ""
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "/") {
			if defaultConv == "" {
				say("!! set a conversation first with /conv, /to or /g")
				continue
			}
			send(envelope{Type: "chat", ConvID: defaultConv, Payload: text(line)})
			continue
		}
		cmd, rest, _ := strings.Cut(line, " ")
		switch cmd {
		case "/conv":
			defaultConv = strings.TrimSpace(rest)
		case "/to":
			uidStr, msg, _ := strings.Cut(rest, " ")
			uid, err := strconv.ParseInt(uidStr, 10, 64)
			if err != nil {
				say("!! bad uid %q", uidStr)
				continue
			}
			defaultConv = privateConv(me, uid)
			send(envelope{Type: "chat", ConvID: defaultConv, Payload: text(msg)})
		case "/g":
			gid, msg, _ := strings.Cut(rest, " ")
			defaultConv = "g_" + gid
			send(envelope{Type: "chat", ConvID: defaultConv, Payload: text(msg)})
		case "/file":
			f := strings.Fields(rest)
			if len(f) != 5 {
				say("!! usage: /file <conv_id> <url> <name> <size> <mime>")
				continue
			}
			size, _ := strconv.ParseInt(f[3], 10, 64)
			p, _ := json.Marshal(map[string]any{"url": f[1], "name": f[2], "size": size, "mime": f[4]})
			send(envelope{Type: "file", ConvID: f[0], Payload: p})
		case "/call":
			parts := strings.SplitN(rest, " ", 3)
			if len(parts) < 2 {
				say("!! usage: /call <kind> <uid> [payload-json]")
				continue
			}
			typ := parts[0]
			if !strings.HasPrefix(typ, "call_") {
				typ = "call_" + typ
			}
			to, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				say("!! bad uid %q", parts[1])
				continue
			}
			var p json.RawMessage
			if len(parts) == 3 {
				p = json.RawMessage(parts[2])
			}
			send(envelope{Type: typ, ConvID: privateConv(me, to), To: to, Payload: p})
		case "/raw":
			if err := conn.WriteMessage(websocket.TextMessage, []byte(rest)); err != nil {
				say("!! write: %v", err)
			} else {
				say(">> %s", rest)
			}
		case "/sleep":
			ms, _ := strconv.Atoi(strings.TrimSpace(rest))
			time.Sleep(time.Duration(ms) * time.Millisecond)
		case "/quit":
			return
		default:
			say("!! unknown command %s", cmd)
		}
	}

	select {
	case <-done:
	case <-time.After(*linger):
	}
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

func privateConv(a, b int64) string {
	if a > b {
		a, b = b, a
	}
	return fmt.Sprintf("u_%d_%d", a, b)
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func postJSON(u string, v any) (int, []byte, error) {
	b, _ := json.Marshal(v)
	resp, err := http.Post(u, "application/json", bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}
