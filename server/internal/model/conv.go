package model

import (
	"fmt"
	"strconv"
	"strings"
)

// conv_id 的格式规则放在 model 里，service（鉴权）和 ws（投递）共用同一份解析。

func PrivateConvID(a, b int64) string {
	if a > b {
		a, b = b, a
	}
	return fmt.Sprintf("u_%d_%d", a, b)
}

func GroupConvID(groupID int64) string {
	return fmt.Sprintf("g_%d", groupID)
}

// ParsePrivateConvID 要求规范形式（小 id 在前、两者不同），这样同一对用户只有一个 conv_id。
func ParsePrivateConvID(convID string) (a, b int64, ok bool) {
	parts := strings.Split(convID, "_")
	if len(parts) != 3 || parts[0] != "u" {
		return 0, 0, false
	}
	a, errA := strconv.ParseInt(parts[1], 10, 64)
	b, errB := strconv.ParseInt(parts[2], 10, 64)
	if errA != nil || errB != nil || a <= 0 || a >= b {
		return 0, 0, false
	}
	return a, b, true
}

func ParseGroupConvID(convID string) (int64, bool) {
	rest, found := strings.CutPrefix(convID, "g_")
	if !found {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
