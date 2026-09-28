package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
)

func seedCmd(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	nUsers := fs.Int("users", 1000, "number of users u0001..uNNNN")
	groupSize := fs.Int("group-size", 20, "members per group")
	api := fs.String("api", "http://localhost:8080", "HTTP base URL")
	password := fs.String("password", "loadtest123", "password for every user")
	out := fs.String("out", "loadtest/fixture.json", "fixture output path")
	conc := fs.Int("concurrency", 32, "parallel HTTP requests")
	_ = fs.Parse(args)
	if *nUsers <= 0 || *groupSize <= 0 {
		return fmt.Errorf("users and group-size must be > 0")
	}

	users := make([]fixtureUser, *nUsers)
	tokens := make([]string, *nUsers)
	var failed atomic.Int64
	var firstErr error
	var errOnce sync.Once
	parallel(*nUsers, *conc, func(i int) {
		name := fmt.Sprintf("u%04d", i+1)
		st, err := doJSON(http.MethodPost, *api+"/api/register", "", map[string]string{"username": name, "password": *password}, nil)
		if err != nil && st != http.StatusConflict { // 重复 seed 时用户已存在，直接登录
			failed.Add(1)
			errOnce.Do(func() { firstErr = err })
			return
		}
		tok, id, err := login(*api, name, *password)
		if err != nil {
			failed.Add(1)
			errOnce.Do(func() { firstErr = err })
			return
		}
		users[i] = fixtureUser{ID: id, Username: name}
		tokens[i] = tok
	})
	if n := failed.Load(); n > 0 {
		return fmt.Errorf("%d users failed to register/login, first error: %v", n, firstErr)
	}
	log.Printf("registered/logged in %d users", *nUsers)

	nGroups := (*nUsers + *groupSize - 1) / *groupSize
	groups := make([]fixtureGroup, nGroups)
	parallel(nGroups, *conc, func(g int) {
		lo := g * *groupSize
		hi := min(lo+*groupSize, *nUsers)
		var created struct {
			ID int64 `json:"id"`
		}
		if _, err := doJSON(http.MethodPost, *api+"/api/groups", tokens[lo], map[string]string{"name": fmt.Sprintf("lt-%d", g+1)}, &created); err != nil {
			failed.Add(1)
			errOnce.Do(func() { firstErr = err })
			return
		}
		members := []int64{users[lo].ID}
		for i := lo + 1; i < hi; i++ {
			if _, err := doJSON(http.MethodPost, fmt.Sprintf("%s/api/groups/%d/join", *api, created.ID), tokens[i], nil, nil); err != nil {
				failed.Add(1)
				errOnce.Do(func() { firstErr = err })
				return
			}
			members = append(members, users[i].ID)
		}
		groups[g] = fixtureGroup{ID: created.ID, Members: members}
	})
	if n := failed.Load(); n > 0 {
		return fmt.Errorf("%d group operations failed, first error: %v", n, firstErr)
	}

	f := fixture{API: *api, Password: *password, GroupSize: *groupSize, Users: users, Groups: groups}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	log.Printf("created %d groups of up to %d members → %s", nGroups, *groupSize, *out)
	return nil
}
