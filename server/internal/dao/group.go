package dao

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"im/internal/model"
)

type GroupDAO struct {
	db *gorm.DB
}

func NewGroupDAO(db *gorm.DB) *GroupDAO {
	return &GroupDAO{db: db}
}

// CreateWithOwner 在同一个事务里建群并把创建者加进去，避免出现没有成员的群。
func (d *GroupDAO) CreateWithOwner(g *model.Group) error {
	return translate(d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(g).Error; err != nil {
			return err
		}
		return tx.Create(&model.GroupMember{GroupID: g.ID, UserID: g.OwnerID}).Error
	}))
}

func (d *GroupDAO) GetByID(id int64) (*model.Group, error) {
	var g model.Group
	if err := d.db.Take(&g, id).Error; err != nil {
		return nil, translate(err)
	}
	return &g, nil
}

// AddMember 重复加入不报错。
func (d *GroupDAO) AddMember(groupID, userID int64) error {
	err := d.db.Clauses(clause.Insert{Modifier: "IGNORE"}).
		Create(&model.GroupMember{GroupID: groupID, UserID: userID}).Error
	return translate(err)
}

func (d *GroupDAO) IsMember(groupID, userID int64) (bool, error) {
	var n int64
	err := d.db.Model(&model.GroupMember{}).
		Where("group_id = ? AND user_id = ?", groupID, userID).Count(&n).Error
	return n > 0, translate(err)
}

func (d *GroupDAO) MemberIDs(groupID int64) ([]int64, error) {
	var ids []int64
	err := d.db.Model(&model.GroupMember{}).
		Where("group_id = ?", groupID).Order("user_id").Pluck("user_id", &ids).Error
	return ids, translate(err)
}

func (d *GroupDAO) ListByUser(userID int64) ([]model.Group, error) {
	var gs []model.Group
	err := d.db.Model(&model.Group{}).
		Joins("JOIN group_members gm ON gm.group_id = `groups`.id").
		Where("gm.user_id = ?", userID).
		Order("`groups`.id").Find(&gs).Error
	return gs, translate(err)
}
