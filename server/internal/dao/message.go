package dao

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"im/internal/model"
)

type MessageDAO struct {
	db *gorm.DB
}

func NewMessageDAO(db *gorm.DB) *MessageDAO {
	return &MessageDAO{db: db}
}

func (d *MessageDAO) ListAfter(convID string, afterSeq int64, limit int) ([]model.Message, error) {
	var ms []model.Message
	err := d.db.Where("conv_id = ? AND seq > ?", convID, afterSeq).
		Order("seq").Limit(limit).Find(&ms).Error
	return ms, translate(err)
}

// MaxSeq 用来在 local 模式启动时接上之前的顺序号。
func (d *MessageDAO) MaxSeq() (int64, error) {
	var max int64
	err := d.db.Model(&model.Message{}).Select("COALESCE(MAX(seq), 0)").Scan(&max).Error
	return max, translate(err)
}

// BatchInsertIgnore 用 INSERT IGNORE：Kafka 重投或客户端用同一个 client_msg_id 重发时，
// 重复行直接跳过而不是让整批失败。
func (d *MessageDAO) BatchInsertIgnore(msgs []model.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	return translate(d.db.Clauses(clause.Insert{Modifier: "IGNORE"}).Create(&msgs).Error)
}
