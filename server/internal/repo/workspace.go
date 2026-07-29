package repo

import (
	"errors"

	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"gorm.io/gorm"
)

// ErrWorkspaceNotFound is returned when a workspace lookup yields no result
// (either missing or not owned by the requesting user).
var ErrWorkspaceNotFound = errors.New("workspace not found")

// CreateWorkspace persists a new workspace.
func CreateWorkspace(db *gorm.DB, w *model.Workspace) error {
	return db.Create(w).Error
}

// ListWorkspaces returns all workspaces owned by a user, newest first.
func ListWorkspaces(db *gorm.DB, userID uint) ([]model.Workspace, error) {
	var list []model.Workspace
	if err := db.Where("user_id = ?", userID).Order("created_at desc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GetWorkspaceByKey looks up a workspace by (user_id, workspace_key). A
// cross-user lookup returns ErrWorkspaceNotFound to avoid leaking existence.
func GetWorkspaceByKey(db *gorm.DB, userID uint, key string) (*model.Workspace, error) {
	var w model.Workspace
	if err := db.Where("user_id = ? AND workspace_key = ?", userID, key).First(&w).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkspaceNotFound
		}
		return nil, err
	}
	return &w, nil
}

// GetWorkspaceByID looks up a workspace by primary key and verifies ownership.
// A missing row or a row owned by another user returns ErrWorkspaceNotFound.
func GetWorkspaceByID(db *gorm.DB, userID, id uint) (*model.Workspace, error) {
	var w model.Workspace
	if err := db.First(&w, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkspaceNotFound
		}
		return nil, err
	}
	if w.UserID != userID {
		return nil, ErrWorkspaceNotFound
	}
	return &w, nil
}

// UpdateWorkspace writes changes to an existing workspace.
func UpdateWorkspace(db *gorm.DB, w *model.Workspace) error {
	return db.Save(w).Error
}

// DeleteWorkspace removes a workspace by primary key.
func DeleteWorkspace(db *gorm.DB, id uint) error {
	return db.Delete(&model.Workspace{}, id).Error
}
