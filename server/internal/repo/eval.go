package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// 本文件是「评估回归」模块（M5-05）的唯一持久化出口，全部按 user_id 归属隔离
// （owner-scoped CRUD），与 MCP / Provider / Workspace 等管理面资源一致。

// ---- 评估集 EvalDataset ----

var ErrEvalDatasetNotFound = errors.New("eval dataset not found")

// CreateEvalDataset 持久化一个新的评估集。
func CreateEvalDataset(db *gorm.DB, d *model.EvalDataset) error {
	return db.Create(d).Error
}

// ListEvalDatasets 返回某用户归属的全部评估集（按创建时间倒序）。
func ListEvalDatasets(db *gorm.DB, userID uint) ([]model.EvalDataset, error) {
	var list []model.EvalDataset
	if err := db.Where("user_id = ?", userID).Order("created_at desc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GetEvalDatasetByName 按 (user_id, name) 查重，用于创建前的冲突检测。
func GetEvalDatasetByName(db *gorm.DB, userID uint, name string) (*model.EvalDataset, error) {
	var d model.EvalDataset
	if err := db.Where("user_id = ? AND name = ?", userID, name).First(&d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEvalDatasetNotFound
		}
		return nil, err
	}
	return &d, nil
}

// GetEvalDataset 按主键查并校验归属；缺失或越权返回 ErrEvalDatasetNotFound。
func GetEvalDataset(db *gorm.DB, userID, id uint) (*model.EvalDataset, error) {
	var d model.EvalDataset
	if err := db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEvalDatasetNotFound
		}
		return nil, err
	}
	if d.UserID != userID {
		return nil, ErrEvalDatasetNotFound
	}
	return &d, nil
}

// UpdateEvalDataset 写入已存在评估集的变更。
func UpdateEvalDataset(db *gorm.DB, d *model.EvalDataset) error {
	return db.Save(d).Error
}

// DeleteEvalDataset 删除评估集，并级联清理其用例、运行与结果（避免孤儿数据）。
func DeleteEvalDataset(db *gorm.DB, id uint) error {
	// 先清结果（按 dataset 维度），再清运行、用例，最后清数据集本身。
	if err := db.Where("dataset_id = ?", id).Delete(&model.EvalResult{}).Error; err != nil {
		return err
	}
	if err := db.Where("dataset_id = ?", id).Delete(&model.EvalRun{}).Error; err != nil {
		return err
	}
	if err := db.Where("dataset_id = ?", id).Delete(&model.EvalCase{}).Error; err != nil {
		return err
	}
	return db.Delete(&model.EvalDataset{}, id).Error
}

// ---- 用例 EvalCase ----

var ErrEvalCaseNotFound = errors.New("eval case not found")

// CreateEvalCase 持久化一个用例。
func CreateEvalCase(db *gorm.DB, c *model.EvalCase) error {
	return db.Create(c).Error
}

// ListEvalCases 返回某评估集下的全部用例（按创建时间正序，保证运行顺序稳定）。
func ListEvalCases(db *gorm.DB, datasetID uint) ([]model.EvalCase, error) {
	var list []model.EvalCase
	if err := db.Where("dataset_id = ?", datasetID).Order("created_at asc, id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GetEvalCase 按 (dataset_id, case_id) 查用例（校验归属到评估集，避免越权访问他人用例）。
func GetEvalCase(db *gorm.DB, datasetID, caseID uint) (*model.EvalCase, error) {
	var c model.EvalCase
	if err := db.Where("dataset_id = ? AND id = ?", datasetID, caseID).First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEvalCaseNotFound
		}
		return nil, err
	}
	return &c, nil
}

// UpdateEvalCase 写入已存在用例的变更。
func UpdateEvalCase(db *gorm.DB, c *model.EvalCase) error {
	return db.Save(c).Error
}

// DeleteEvalCase 删除一个用例，并清理其全部运行结果。
func DeleteEvalCase(db *gorm.DB, datasetID, caseID uint) error {
	if err := db.Where("dataset_id = ? AND case_id = ?", datasetID, caseID).Delete(&model.EvalResult{}).Error; err != nil {
		return err
	}
	return db.Where("dataset_id = ? AND id = ?", datasetID, caseID).Delete(&model.EvalCase{}).Error
}

// ---- 运行 EvalRun ----

var ErrEvalRunNotFound = errors.New("eval run not found")

// CreateEvalRun 持久化一条运行记录。
func CreateEvalRun(db *gorm.DB, r *model.EvalRun) error {
	return db.Create(r).Error
}

// GetEvalRun 按主键查并校验归属；缺失或越权返回 ErrEvalRunNotFound。
func GetEvalRun(db *gorm.DB, userID, id uint) (*model.EvalRun, error) {
	var r model.EvalRun
	if err := db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEvalRunNotFound
		}
		return nil, err
	}
	if r.UserID != userID {
		return nil, ErrEvalRunNotFound
	}
	return &r, nil
}

// ListEvalRuns 返回某用户归属的运行记录（可按 dataset_id 过滤），按创建时间倒序。
func ListEvalRuns(db *gorm.DB, userID, datasetID uint) ([]model.EvalRun, error) {
	q := db.Where("user_id = ?", userID)
	if datasetID != 0 {
		q = q.Where("dataset_id = ?", datasetID)
	}
	var list []model.EvalRun
	if err := q.Order("created_at desc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateEvalRun 写入运行记录的最终聚合结果。
func UpdateEvalRun(db *gorm.DB, r *model.EvalRun) error {
	return db.Save(r).Error
}

// ---- 结果 EvalResult ----

// CreateEvalResult 持久化单条尝试结果。
func CreateEvalResult(db *gorm.DB, r *model.EvalResult) error {
	return db.Create(r).Error
}

// ListEvalResults 返回某运行下的全部尝试结果（按用例、尝试序号正序，便于前端逐条展示）。
func ListEvalResults(db *gorm.DB, runID uint) ([]model.EvalResult, error) {
	var list []model.EvalResult
	if err := db.Where("run_id = ?", runID).Order("case_id asc, attempt asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
