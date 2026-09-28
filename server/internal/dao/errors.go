package dao

import (
	"errors"

	"gorm.io/gorm"
)

// 上层只认这两个错误，不需要知道 gorm / MySQL 的错误类型。
var (
	ErrNotFound  = errors.New("dao: not found")
	ErrDuplicate = errors.New("dao: duplicate key")
)

func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return ErrNotFound
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return ErrDuplicate
	}
	return err
}
