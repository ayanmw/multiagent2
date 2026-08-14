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

// ListUsers returns all users ordered by id (with role preloaded).
// Used by the admin user-management view.
func ListUsers(db *gorm.DB) ([]model.User, error) {
	var users []model.User
	if err := db.Preload("Role").Order("id asc").Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// UpdateUser persists changes to an existing user (role, status, display name...).
func UpdateUser(db *gorm.DB, user *model.User) error {
	return db.Save(user).Error
}

// CountAdmins returns the number of active admin users.
// Used to prevent an admin from disabling/demoting themselves into a lockout.
func CountAdmins(db *gorm.DB) (int64, error) {
	roleID, err := GetRoleIDByName(db, model.RoleAdmin)
	if err != nil {
		return 0, err
	}
	var cnt int64
	if err := db.Model(&model.User{}).Where("role_id = ? AND status = ?", roleID, model.UserStatusActive).
		Count(&cnt).Error; err != nil {
		return 0, err
	}
	return cnt, nil
}
