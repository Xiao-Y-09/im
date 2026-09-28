package service

import (
	"im/internal/dao"
	"im/internal/model"
)

type ConvService struct {
	groups *dao.GroupDAO
}

func NewConvService(groups *dao.GroupDAO) *ConvService {
	return &ConvService{groups: groups}
}

// CanAccess 对格式错误的 conv_id 返回 bad_request，对无权限返回 forbidden。
func (s *ConvService) CanAccess(uid int64, convID string) error {
	if a, b, ok := model.ParsePrivateConvID(convID); ok {
		if uid != a && uid != b {
			return newErr(CodeForbidden, "not a participant of this conversation")
		}
		return nil
	}
	if gid, ok := model.ParseGroupConvID(convID); ok {
		member, err := s.groups.IsMember(gid, uid)
		if err != nil {
			return internalErr(err)
		}
		if !member {
			return newErr(CodeForbidden, "not a member of this group")
		}
		return nil
	}
	return newErr(CodeBadRequest, "malformed conv_id")
}
