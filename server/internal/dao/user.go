package dao

import (
	"strings"

	"gorm.io/gorm"

	"im/internal/model"
)

type UserDAO struct {
	db *gorm.DB
}

func NewUserDAO(db *gorm.DB) *UserDAO {
	return &UserDAO{db: db}
}

func (d *UserDAO) Create(u *model.User) error {
	return translate(d.db.Create(u).Error)
}

func (d *UserDAO) GetByUsername(username string) (*model.User, error) {
	var u model.User
	if err := d.db.Where("username = ?", username).Take(&u).Error; err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

func (d *UserDAO) GetByID(id int64) (*model.User, error) {
	var u model.User
	if err := d.db.Take(&u, id).Error; err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

func (d *UserDAO) GetByIDs(ids []int64) ([]model.User, error) {
	var us []model.User
	if len(ids) == 0 {
		return us, nil
	}
	err := d.db.Select("id", "username", "created_at").Where("id IN ?", ids).Find(&us).Error
	return us, translate(err)
}

// SearchPrefix 用前缀匹配，才能走 username 唯一索引。
func (d *UserDAO) SearchPrefix(prefix string, limit int) ([]model.User, error) {
	var us []model.User
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	err := d.db.Select("id", "username", "created_at").
		Where("username LIKE ?", escaped+"%").
		Order("username").Limit(limit).Find(&us).Error
	return us, translate(err)
}
