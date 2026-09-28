package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

type fixtureUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type fixtureGroup struct {
	ID      int64   `json:"id"`
	Members []int64 `json:"members"`
}

type fixture struct {
	API       string         `json:"api"`
	Password  string         `json:"password"`
	GroupSize int            `json:"group_size"`
	Users     []fixtureUser  `json:"users"`
	Groups    []fixtureGroup `json:"groups"`
}

func doJSON(method, target, token string, in, out any) (int, error) {
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, target, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if out != nil && resp.StatusCode/100 == 2 {
		if err := json.Unmarshal(body, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s: %w", target, err)
		}
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, target, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.StatusCode, nil
}

func login(api, username, password string) (string, int64, error) {
	var lr struct {
		Token string `json:"token"`
		User  struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	_, err := doJSON(http.MethodPost, api+"/api/login", "", map[string]string{"username": username, "password": password}, &lr)
	return lr.Token, lr.User.ID, err
}

// httpBase 把 ws://host:port/ws 变成 http://host:port。
func httpBase(wsURL string) (string, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return "", err
	}
	scheme := "http"
	if u.Scheme == "wss" {
		scheme = "https"
	}
	return scheme + "://" + u.Host, nil
}

// parallel 以 n 路并发执行 fn(0..total-1)，错误由 fn 自己记录。
func parallel(total, n int, fn func(i int)) {
	jobs := make(chan int)
	done := make(chan struct{})
	for w := 0; w < n; w++ {
		go func() {
			for i := range jobs {
				fn(i)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < total; i++ {
		jobs <- i
	}
	close(jobs)
	for w := 0; w < n; w++ {
		<-done
	}
}
