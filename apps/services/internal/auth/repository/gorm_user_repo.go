// Package repository implements domain repository interfaces.
package repository

import (
	"context"
	"errors"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/domain"
	"gorm.io/gorm"
)

// gormUserRepo implements domain.UserRepository using gorm.
type gormUserRepo struct {
	db *gorm.DB
}

// NewGormUserRepo creates a new gorm-backed UserRepository.
func NewGormUserRepo(db *gorm.DB) domain.UserRepository {
	return &gormUserRepo{db: db}
}

// FindByUsername looks up a user by username. Returns nil, nil if not found.
func (r *gormUserRepo) FindByUsername(ctx context.Context, username string) (*domain.User, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}
