package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// ErrUserNotFound is returned when a user lookup yields no result.
var ErrUserNotFound = errors.New("user not found")

// CreateUser persists a new user.
func CreateUser(db *gorm.DB, user *model.User) error {
	return db.Create(user).Error
}

// GetUserByUsername finds a user by unique username (with role preloaded).
func GetUserByUsername(db *gorm.DB, username string) (*model.User, error) {
	var u model.User
	if err := db.Preload("Role").Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByEmail finds a user by unique email (with role preloaded).
func GetUserByEmail(db *gorm.DB, email string) (*model.User, error) {
	var u model.User
	if err := db.Preload("Role").Where("email = ?", email).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByID finds a user by primary key (with role preloaded).
func GetUserByID(db *gorm.DB, id uint) (*model.User, error) {
	var u model.User
	if err := db.Preload("Role").First(&u, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}
