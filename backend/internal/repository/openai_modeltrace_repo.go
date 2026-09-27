package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// openaiModelTraceRepository 降智检测（modeltrace）数据访问：样本、指纹库、结果。
// 纯 SQL 实现（表由 migrations/241 创建，不经 ent schema）。
type openaiModelTraceRepository struct{ db *sql.DB }

// NewOpenAIModelTraceRepository 构造降智检测仓库。
func NewOpenAIModelTraceRepository(db *sql.DB) service.CodexModelTraceRepository {
	return &openaiModelTraceRepository{db: db}
}

func (r *openaiModelTraceRepository) InsertSample(ctx context.Context, s *service.CodexModelTraceSample) error {
	raw, err := json.Marshal(s.Numbers)
	if err != nil {
		return fmt.Errorf("marshal numbers: %w", err)
	}
	return r.db.QueryRowContext(ctx, `
		INSERT INTO codex_modeltrace_samples (
			model, account_id, expected_count, numbers, numbers_count, latency_ms
		) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at`,
		s.Model, s.AccountID, s.ExpectedCount, raw, len(s.Numbers), s.LatencyMS,
	).Scan(&s.ID, &s.CreatedAt)
}

func (r *openaiModelTraceRepository) ListSamples(ctx context.Context, model string, limit int) ([]*service.CodexModelTraceSample, error) {
	if limit <= 0 || limit > 100000 {
		limit = 100000
	}
	args := []any{limit}
	query := `
		SELECT id, model, account_id, expected_count, numbers, numbers_count, latency_ms, created_at
		FROM codex_modeltrace_samples`
	if model != "" {
		query += ` WHERE model = $2`
		args = append(args, model)
	}
	query += ` ORDER BY id LIMIT $1`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list modeltrace samples: %w", err)
	}
	defer rows.Close()
	var out []*service.CodexModelTraceSample
	for rows.Next() {
		var s service.CodexModelTraceSample
		var numbersRaw []byte
		if err := rows.Scan(&s.ID, &s.Model, &s.AccountID, &s.ExpectedCount, &numbersRaw,
			&s.NumbersCount, &s.LatencyMS, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan modeltrace sample: %w", err)
		}
		if err := json.Unmarshal(numbersRaw, &s.Numbers); err != nil {
			return nil, fmt.Errorf("decode numbers: %w", err)
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (r *openaiModelTraceRepository) DeleteSamples(ctx context.Context, model string) (int64, error) {
	var n int64
	var err error
	if model == "" {
		err = r.db.QueryRowContext(ctx, `DELETE FROM codex_modeltrace_samples`).Scan(&n)
	} else {
		err = r.db.QueryRowContext(ctx, `DELETE FROM codex_modeltrace_samples WHERE model = $1`, model).Scan(&n)
	}
	if err != nil {
		return 0, fmt.Errorf("delete modeltrace samples: %w", err)
	}
	return n, nil
}

func (r *openaiModelTraceRepository) SaveBank(ctx context.Context, bankJSON string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO codex_modeltrace_bank (id, bank, updated_at)
		VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE SET bank = EXCLUDED.bank, updated_at = NOW()`,
		bankJSON)
	if err != nil {
		return fmt.Errorf("save modeltrace bank: %w", err)
	}
	return nil
}

func (r *openaiModelTraceRepository) GetBank(ctx context.Context) (string, time.Time, bool, error) {
	var bankJSON string
	var updatedAt time.Time
	err := r.db.QueryRowContext(ctx, `SELECT bank, updated_at FROM codex_modeltrace_bank WHERE id = 1`).
		Scan(&bankJSON, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("get modeltrace bank: %w", err)
	}
	return bankJSON, updatedAt, true, nil
}

func (r *openaiModelTraceRepository) InsertResult(ctx context.Context, res *service.CodexModelTraceResult) error {
	return r.db.QueryRowContext(ctx, `
		INSERT INTO codex_modeltrace_results (
			task_id, kind, account_id, model, verdict, probability, "match",
			top_hits, valid_runs, failures, avg_latency_ms, reasons
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id, created_at`,
		res.TaskID, res.Kind, res.AccountID, res.Model, res.Verdict, res.Probability, res.Match,
		res.TopHits, res.ValidRuns, res.Failures, res.AvgLatencyMS, res.Reasons,
	).Scan(&res.ID, &res.CreatedAt)
}

func (r *openaiModelTraceRepository) ListResults(ctx context.Context, taskID string, limit, offset int) ([]*service.CodexModelTraceResult, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	args := []any{limit, offset}
	query := `
		SELECT id, task_id, kind, account_id, model, verdict, probability, "match",
		       top_hits, valid_runs, failures, avg_latency_ms, reasons, created_at
		FROM codex_modeltrace_results`
	if taskID != "" {
		query += ` WHERE task_id = $3`
		args = append(args, taskID)
	}
	query += ` ORDER BY id DESC LIMIT $1 OFFSET $2`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list modeltrace results: %w", err)
	}
	defer rows.Close()
	var out []*service.CodexModelTraceResult
	for rows.Next() {
		var res service.CodexModelTraceResult
		if err := rows.Scan(&res.ID, &res.TaskID, &res.Kind, &res.AccountID, &res.Model, &res.Verdict,
			&res.Probability, &res.Match, &res.TopHits, &res.ValidRuns, &res.Failures,
			&res.AvgLatencyMS, &res.Reasons, &res.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan modeltrace result: %w", err)
		}
		out = append(out, &res)
	}
	return out, rows.Err()
}
