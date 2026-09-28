package service

import (
	"errors"
	"strings"
	"unicode/utf8"

	"im/internal/dao"
	"im/internal/model"
)

// GroupCacheInvalidator 由 ws.Hub 实现。用接口注入，service 就不必依赖 ws 包。
type GroupCacheInvalidator interface {
	InvalidateGroup(groupID int64)
}

type GroupService struct {
	groups      *dao.GroupDAO
	users       *dao.UserDAO
	invalidator GroupCacheInvalidator
}

func NewGroupService(groups *dao.GroupDAO, users *dao.UserDAO) *GroupService {
	return &GroupService{groups: groups, users: users}
}

// SetInvalidator 单独注入，因为 Hub 自己也依赖 GroupService（取成员），两者只能先后构造。
func (s *GroupService) SetInvalidator(inv GroupCacheInvalidator) {
	s.invalidator = inv
}

type GroupView struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	OwnerID int64  `json:"owner_id"`
}

func groupView(g *model.Group) GroupView {
	return GroupView{ID: g.ID, Name: g.Name, OwnerID: g.OwnerID}
}

func (s *GroupService) Create(uid int64, name string) (GroupView, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return GroupView{}, newErr(CodeBadRequest, "group name must be 1-64 characters")
	}
	g := &model.Group{Name: name, OwnerID: uid}
	if err := s.groups.CreateWithOwner(g); err != nil {
		return GroupView{}, internalErr(err)
	}
	return groupView(g), nil
}

func (s *GroupService) Join(uid, groupID int64) (GroupView, error) {
	g, err := s.groups.GetByID(groupID)
	if err != nil {
		if errors.Is(err, dao.ErrNotFound) {
			return GroupView{}, newErr(CodeNotFound, "group not found")
		}
		return GroupView{}, internalErr(err)
	}
	if err := s.groups.AddMember(groupID, uid); err != nil {
		return GroupView{}, internalErr(err)
	}
	if s.invalidator != nil {
		s.invalidator.InvalidateGroup(groupID)
	}
	return groupView(g), nil
}

// Members 只有群成员能看。
func (s *GroupService) Members(uid, groupID int64) ([]UserView, error) {
	ok, err := s.groups.IsMember(groupID, uid)
	if err != nil {
		return nil, internalErr(err)
	}
	if !ok {
		return nil, newErr(CodeForbidden, "not a member of this group")
	}
	ids, err := s.groups.MemberIDs(groupID)
	if err != nil {
		return nil, internalErr(err)
	}
	us, err := s.users.GetByIDs(ids)
	if err != nil {
		return nil, internalErr(err)
	}
	return views(us), nil
}

func (s *GroupService) MyGroups(uid int64) ([]GroupView, error) {
	gs, err := s.groups.ListByUser(uid)
	if err != nil {
		return nil, internalErr(err)
	}
	out := make([]GroupView, 0, len(gs))
	for i := range gs {
		out = append(out, groupView(&gs[i]))
	}
	return out, nil
}

// MemberIDs 不做权限检查，只给 Hub 投递用。
func (s *GroupService) MemberIDs(groupID int64) ([]int64, error) {
	return s.groups.MemberIDs(groupID)
}
