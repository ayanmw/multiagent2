package api

import (
	"context"
)

// PromptIterRun 与后端 server/internal/api/promptiter.go 视图对齐（GEPA 优化运行记录）。
type PromptIterRun struct {
	ID              uint     `json:"id"`
	UserID          uint     `json:"user_id"`
	DatasetID       uint     `json:"dataset_id"`
	InstructionName string   `json:"instruction_name"`
	Role            string   `json:"role"`
	Repeats         int      `json:"repeats"`
	Threshold       float64  `json:"threshold"`
	Status          string   `json:"status"`
	Error           string   `json:"error"`
	BaselineScore   float64  `json:"baseline_score"`
	CandidateScore  float64  `json:"candidate_score"`
	BeforeContent   string   `json:"before_content"`
	AfterContent    string   `json:"after_content"`
	Reasoning       string   `json:"reasoning"`
	WeakCount       int      `json:"weak_count"`
	CreatedAt       string   `json:"created_at"`
	FinishedAt      *string  `json:"finished_at"`
}

// Instruction 与后端 AgentInstruction 视图对齐。
type Instruction struct {
	ID        uint    `json:"id"`
	UserID    uint    `json:"user_id"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	Content   string  `json:"content"`
	Version   int     `json:"version"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// OptimizePromptIter 调 POST /api/promptiter/optimize（异步；返回运行记录）。
func (c *Client) OptimizePromptIter(ctx context.Context, datasetID uint, instructionName string, role string, repeats int, threshold float64) (*PromptIterRun, error) {
	body := map[string]any{"dataset_id": datasetID, "repeats": repeats, "threshold": threshold}
	if instructionName != "" {
		body["instruction_name"] = instructionName
	}
	if role != "" {
		body["role"] = role
	}
	var r PromptIterRun
	if err := c.do(ctx, "POST", "/promptiter/optimize", body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListPromptIterRuns 调 GET /api/promptiter/runs。
func (c *Client) ListPromptIterRuns(ctx context.Context) ([]PromptIterRun, error) {
	var r struct {
		Runs []PromptIterRun `json:"runs"`
	}
	if err := c.do(ctx, "GET", "/promptiter/runs", nil, &r); err != nil {
		return nil, err
	}
	return r.Runs, nil
}

// GetPromptIterRun 调 GET /api/promptiter/runs/:id。
func (c *Client) GetPromptIterRun(ctx context.Context, runID uint) (*PromptIterRun, error) {
	var r PromptIterRun
	if err := c.do(ctx, "GET", "/promptiter/runs/"+itoa(runID), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// RollbackPromptIter 调 POST /api/promptiter/runs/:id/rollback。
func (c *Client) RollbackPromptIter(ctx context.Context, runID uint) (*PromptIterRun, error) {
	var r PromptIterRun
	if err := c.do(ctx, "POST", "/promptiter/runs/"+itoa(runID)+"/rollback", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListInstructions 调 GET /api/instructions。
func (c *Client) ListInstructions(ctx context.Context) ([]Instruction, error) {
	var r struct {
		Instructions []Instruction `json:"instructions"`
	}
	if err := c.do(ctx, "GET", "/instructions", nil, &r); err != nil {
		return nil, err
	}
	return r.Instructions, nil
}

// UpdateInstruction 调 PUT /api/instructions/:name。
func (c *Client) UpdateInstruction(ctx context.Context, name, content, role string) (*Instruction, error) {
	body := map[string]string{"content": content}
	if role != "" {
		body["role"] = role
	}
	var r Instruction
	if err := c.do(ctx, "PUT", "/instructions/"+name, body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
