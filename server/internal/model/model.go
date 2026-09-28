package model

import "time"

type User struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	Username  string    `gorm:"type:varchar(32);not null;uniqueIndex"`
	PwdHash   []byte    `gorm:"type:varbinary(60);not null"`
	CreatedAt time.Time `gorm:"type:datetime(3)"`
}

type Group struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	Name      string    `gorm:"type:varchar(64);not null"`
	OwnerID   int64     `gorm:"not null"`
	CreatedAt time.Time `gorm:"type:datetime(3)"`
}

type GroupMember struct {
	GroupID int64 `gorm:"primaryKey;autoIncrement:false"`
	UserID  int64 `gorm:"primaryKey;autoIncrement:false;index:idx_user"`
}

// Message.Seq 不自增：它由 Transport 分配（local 的 dispatcher 或 kafka offset），
// 这样投递和落库看到的是同一个顺序号。
type Message struct {
	Seq         int64     `gorm:"primaryKey;autoIncrement:false;index:idx_conv_seq,priority:2"`
	ConvID      string    `gorm:"type:varchar(40);not null;index:idx_conv_seq,priority:1"`
	SenderID    int64     `gorm:"not null"`
	Type        string    `gorm:"type:varchar(16);not null"`
	Content     string    `gorm:"type:text;not null"`
	ClientMsgID string    `gorm:"type:char(36);not null;uniqueIndex"`
	CreatedAt   time.Time `gorm:"type:datetime(3)"`
}
