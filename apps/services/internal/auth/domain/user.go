// Package domain defines core entities and repository interfaces for the auth bounded context.
package domain

import (
	"context"
	"time"
)

// User maps to the `users` table in MariaDB.
type User struct {
	ID           int       `gorm:"column:id;primaryKey;autoIncrement"`
	Username     string    `gorm:"column:username;size:64;uniqueIndex;not null"`
	PasswordHash string    `gorm:"column:password_hash;size:255;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
}

// TableName overrides gorm default table name.
func (User) TableName() string { return "users" }

// UserRepository abstracts persistence operations for User.
type UserRepository interface {
	FindByUsername(ctx context.Context, username string) (*User, error)
}

// SessionRepository abstracts session storage (Redis).
type SessionRepository interface {
	// Set stores a session mapping jti -> userID with the given TTL.
	Set(ctx context.Context, jti string, userID int, ttl time.Duration) error
}
