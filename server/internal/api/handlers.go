package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"im/internal/service"
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *API) register(c *gin.Context) {
	var req credentials
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "body must be {username, password}")
		return
	}
	u, err := a.users.Register(req.Username, req.Password)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, u)
}

func (a *API) login(c *gin.Context) {
	var req credentials
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "body must be {username, password}")
		return
	}
	tok, u, err := a.users.Login(req.Username, req.Password)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": tok, "user": u})
}

func (a *API) me(c *gin.Context) {
	u, err := a.users.Me(uidOf(c))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

// searchUsers 支持 ?q=前缀 搜人，或 ?ids=1,2,3 批量取用户名（前端展示发送者名字用）。
func (a *API) searchUsers(c *gin.Context) {
	var (
		us  []service.UserView
		err error
	)
	if raw := c.Query("ids"); raw != "" {
		ids, perr := parseIDs(raw)
		if perr != nil {
			badRequest(c, "ids must be comma separated integers")
			return
		}
		us, err = a.users.ByIDs(ids)
	} else {
		us, err = a.users.Search(strings.TrimSpace(c.Query("q")))
	}
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": us})
}

func (a *API) createGroup(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "body must be {name}")
		return
	}
	g, err := a.groups.Create(uidOf(c), req.Name)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, g)
}

func (a *API) joinGroup(c *gin.Context) {
	gid, ok := pathID(c)
	if !ok {
		return
	}
	g, err := a.groups.Join(uidOf(c), gid)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, g)
}

func (a *API) groupMembers(c *gin.Context) {
	gid, ok := pathID(c)
	if !ok {
		return
	}
	ms, err := a.groups.Members(uidOf(c), gid)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": ms})
}

func (a *API) myGroups(c *gin.Context) {
	gs, err := a.groups.MyGroups(uidOf(c))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": gs})
}

func (a *API) history(c *gin.Context) {
	convID := c.Query("conv_id")
	if convID == "" {
		badRequest(c, "conv_id is required")
		return
	}
	afterSeq, err := queryInt(c, "after_seq", 0)
	if err != nil {
		badRequest(c, "after_seq must be an integer")
		return
	}
	limit, err := queryInt(c, "limit", 0)
	if err != nil {
		badRequest(c, "limit must be an integer")
		return
	}
	ms, err := a.messages.History(uidOf(c), convID, afterSeq, int(limit))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": ms})
}

func (a *API) presenceQuery(c *gin.Context) {
	uids, err := parseIDs(c.Query("uids"))
	if err != nil {
		badRequest(c, "uids must be comma separated integers")
		return
	}
	m, err := a.presence.Query(c.Request.Context(), uids)
	if err != nil {
		writeErr(c, err)
		return
	}
	out := make(map[string]bool, len(m))
	for uid, online := range m {
		out[strconv.FormatInt(uid, 10)] = online
	}
	c.JSON(http.StatusOK, out)
}

func pathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		badRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}

func queryInt(c *gin.Context, key string, def int64) (int64, error) {
	raw := c.Query(key)
	if raw == "" {
		return def, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func parseIDs(raw string) ([]int64, error) {
	ids := []int64{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
