package api

import (
	"context"
)

// EvalDataset / EvalCase / EvalRun / EvalResult 与后端 server/internal/api/eval.go 视图对齐。
type EvalDataset struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DefaultGrader string `json:"default_grader"`
	DefaultModel  string `json:"default_model"`
	CreatedAt     string `json:"created_at"`
}

type EvalCase struct {
	ID         uint   `json:"id"`
	DatasetID  uint   `json:"dataset_id"`
	Input      string `json:"input"`
	Expected   string `json:"expected"`
	Grader     string `json:"grader"`
	Model      string `json:"model"`
}

type EvalRun struct {
	ID            uint    `json:"id"`
	DatasetID     uint    `json:"dataset_id"`
	Model         string  `json:"model"`
	Grader        string  `json:"grader"`
	Repeats       int     `json:"repeats"`
	Status        string  `json:"status"`
	ScoreAvg      float64 `json:"score_avg"`
	PassRate      float64 `json:"pass_rate"`
	TotalCases    int     `json:"total_cases"`
	TotalAttempts int     `json:"total_attempts"`
	Error         string  `json:"error"`
	CreatedAt     string  `json:"created_at"`
}

type EvalResult struct {
	ID        uint    `json:"id"`
	RunID     uint    `json:"run_id"`
	CaseID    uint    `json:"case_id"`
	Attempt   int     `json:"attempt"`
	Grader    string  `json:"grader"`
	Output    string  `json:"output"`
	Score     float64 `json:"score"`
	Passed    bool    `json:"passed"`
	LatencyMs int64   `json:"latency_ms"`
	Error     string  `json:"error"`
}

// ListEvalDatasets 调 GET /api/eval/datasets。
func (c *Client) ListEvalDatasets(ctx context.Context) ([]EvalDataset, error) {
	var r struct {
		Datasets []EvalDataset `json:"datasets"`
	}
	if err := c.do(ctx, "GET", "/eval/datasets", nil, &r); err != nil {
		return nil, err
	}
	return r.Datasets, nil
}

// CreateEvalDataset 调 POST /api/eval/datasets。
func (c *Client) CreateEvalDataset(ctx context.Context, name, description, grader, model string) (*EvalDataset, error) {
	var r EvalDataset
	err := c.do(ctx, "POST", "/eval/datasets", map[string]string{
		"name":           name,
		"description":    description,
		"default_grader": grader,
		"default_model":  model,
	}, &r)
	return &r, err
}

// ListEvalCases 调 GET /api/eval/datasets/:id/cases。
func (c *Client) ListEvalCases(ctx context.Context, datasetID uint) ([]EvalCase, error) {
	var r struct {
		Cases []EvalCase `json:"cases"`
	}
	if err := c.do(ctx, "GET", "/eval/datasets/"+itoa(datasetID)+"/cases", nil, &r); err != nil {
		return nil, err
	}
	return r.Cases, nil
}

// RunEval 调 POST /api/eval/datasets/:id/run（异步；返回 running 记录）。
func (c *Client) RunEval(ctx context.Context, datasetID uint, model, grader string, repeats int) (*EvalRun, error) {
	body := map[string]any{"repeats": repeats}
	if model != "" {
		body["model"] = model
	}
	if grader != "" {
		body["grader"] = grader
	}
	var r EvalRun
	err := c.do(ctx, "POST", "/eval/datasets/"+itoa(datasetID)+"/run", body, &r)
	return &r, err
}

// ListEvalRuns 调 GET /api/eval/runs?dataset_id=。
func (c *Client) ListEvalRuns(ctx context.Context, datasetID uint) ([]EvalRun, error) {
	path := "/eval/runs"
	if datasetID != 0 {
		path += "?dataset_id=" + itoa(datasetID)
	}
	var r struct {
		Runs []EvalRun `json:"runs"`
	}
	if err := c.do(ctx, "GET", path, nil, &r); err != nil {
		return nil, err
	}
	return r.Runs, nil
}

// GetEvalRun 调 GET /api/eval/runs/:id。
func (c *Client) GetEvalRun(ctx context.Context, runID uint) (*EvalRun, error) {
	var r EvalRun
	if err := c.do(ctx, "GET", "/eval/runs/"+itoa(runID), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListEvalResults 调 GET /api/eval/runs/:id/results。
func (c *Client) ListEvalResults(ctx context.Context, runID uint) ([]EvalResult, error) {
	var r struct {
		Results []EvalResult `json:"results"`
	}
	if err := c.do(ctx, "GET", "/eval/runs/"+itoa(runID)+"/results", nil, &r); err != nil {
		return nil, err
	}
	return r.Results, nil
}

// itoa 是避免引入 strconv 的极小整数转字符串（CLI 包内复用）。
func itoa(u uint) string {
	if u == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	return string(b[i:])
}
