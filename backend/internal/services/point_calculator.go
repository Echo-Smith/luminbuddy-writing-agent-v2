package services

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// ─── 点数换算服务 ──────────────────────────────────────────
//
// PointCalculator 负责将底层 Token 消耗换算为用户可感知的"点数"。
//
// 换算公式：
//   points = (prompt_tokens × input_rate + completion_tokens × output_rate) × global_multiplier
//
// 费率来源：
//   1. 优先匹配 (model_name, task_type) 精确组合
//   2. 回退到 ('*', task_type) 通配符
//   3. 再回退到 ('*', 'writing') 默认值
//
// 全局倍率：
//   admin 可设 1.5x（促销）或 0.8x（降价），乘以费率表结果

// PointRateSnapshot 是某次换算使用的费率快照（用于审计）
type PointRateSnapshot struct {
	InputRate  float64 `json:"input_rate"`
	OutputRate float64 `json:"output_rate"`
}

// PointCalcResult 是一次换算的结果
type PointCalcResult struct {
	Points           float64          `json:"points"`
	InputRate        float64          `json:"input_rate"`
	OutputRate       float64          `json:"output_rate"`
	GlobalMultiplier float64          `json:"global_multiplier"`
	PromptTokens     int              `json:"prompt_tokens"`
	CompletionTokens int              `json:"completion_tokens"`
	ModelName        string           `json:"model_name"`
	TaskType         string           `json:"task_type"`
}

// rateCacheEntry 缓存一个费率组合
type rateCacheEntry struct {
	inputRate    float64
	outputRate   float64
	loadedAt     time.Time
}

// PointCalculator 管理费率查询和点数换算
type PointCalculator struct {
	db      *database.DB
	cache   sync.Map // key: "model|task" → rateCacheEntry
	cacheTTL time.Duration
}

// NewPointCalculator 创建点数换算器
func NewPointCalculator(db *database.DB) *PointCalculator {
	return &PointCalculator{
		db:       db,
		cacheTTL: 60 * time.Second, // 60 秒缓存，admin 改费率后最多 60 秒生效
	}
}

// GetRate 查询指定模型+操作类型的费率
// 优先匹配精确模型名，回退到通配符 *
func (pc *PointCalculator) GetRate(ctx context.Context, modelName, taskType string) (inputRate, outputRate float64, err error) {
	cacheKey := fmt.Sprintf("%s|%s", modelName, taskType)

	// 尝试缓存
	if entry, ok := pc.cache.Load(cacheKey); ok {
		ce := entry.(rateCacheEntry)
		if time.Since(ce.loadedAt) < pc.cacheTTL {
			return ce.inputRate, ce.outputRate, nil
		}
	}

	if pc.db == nil {
		return 0.001, 0.003, nil // 安全默认值
	}

	// 1. 精确匹配
	inputRate, outputRate, err = pc.queryRate(ctx, modelName, taskType)
	if err == nil && inputRate > 0 {
		pc.cache.Store(cacheKey, rateCacheEntry{inputRate, outputRate, time.Now()})
		return inputRate, outputRate, nil
	}

	// 2. 通配符匹配
	inputRate, outputRate, err = pc.queryRate(ctx, "*", taskType)
	if err == nil && inputRate > 0 {
		pc.cache.Store(cacheKey, rateCacheEntry{inputRate, outputRate, time.Now()})
		return inputRate, outputRate, nil
	}

	// 3. 最终回退
	slog.Warn("point_calculator: no rate found, using default",
		"model", modelName, "task_type", taskType)
	return 0.001, 0.003, nil
}

// queryRate 从数据库查询费率
func (pc *PointCalculator) queryRate(ctx context.Context, modelName, taskType string) (float64, float64, error) {
	var inputRate, outputRate float64
	err := pc.db.QueryRowContext(ctx, `
		SELECT input_rate, output_rate
		FROM point_rates
		WHERE model_name = $1 AND task_type = $2 AND is_active = TRUE
	`, modelName, taskType).Scan(&inputRate, &outputRate)
	return inputRate, outputRate, err
}

// GetGlobalMultiplier 获取全局倍率
func (pc *PointCalculator) GetGlobalMultiplier(ctx context.Context) float64 {
	if pc.db == nil {
		return 1.0
	}
	var multiplier float64
	err := pc.db.QueryRowContext(ctx, `
		SELECT global_multiplier FROM billing_config WHERE id = 1
	`).Scan(&multiplier)
	if err != nil || multiplier <= 0 {
		return 1.0
	}
	return multiplier
}

// Calculate 计算 Token 消耗对应的点数
func (pc *PointCalculator) Calculate(ctx context.Context, promptTokens, completionTokens int, modelName, taskType string) (*PointCalcResult, error) {
	inputRate, outputRate, err := pc.GetRate(ctx, modelName, taskType)
	if err != nil {
		inputRate, outputRate = 0.001, 0.003
	}

	multiplier := pc.GetGlobalMultiplier(ctx)

	points := (float64(promptTokens)*inputRate + float64(completionTokens)*outputRate) * multiplier

	return &PointCalcResult{
		Points:           points,
		InputRate:        inputRate,
		OutputRate:       outputRate,
		GlobalMultiplier: multiplier,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ModelName:        modelName,
		TaskType:         taskType,
	}, nil
}

// PointsPerKToken 计算综合千字点数（用于前端展示"约 X 点/千字"）
// 公式：(inputRate × 输入占比 + outputRate × 输出占比) × 1000 × multiplier
// 经验值：典型写作场景输入:输出 ≈ 2:1
func (pc *PointCalculator) PointsPerKToken(ctx context.Context, modelName, taskType string) (float64, error) {
	inputRate, outputRate, err := pc.GetRate(ctx, modelName, taskType)
	if err != nil {
		inputRate, outputRate = 0.001, 0.003
	}

	multiplier := pc.GetGlobalMultiplier(ctx)

	// 加权混合：输入占 67%，输出占 33%
	blendInput := 0.67
	blendOutput := 0.33

	pointsPerK := (inputRate*blendInput + outputRate*blendOutput) * 1000 * multiplier
	return pointsPerK, nil
}

// InvalidateCache 清除费率缓存（admin 修改费率后调用）
func (pc *PointCalculator) InvalidateCache() {
	pc.cache.Range(func(key, _ interface{}) bool {
		pc.cache.Delete(key)
		return true
	})
	slog.Info("point_calculator: rate cache invalidated")
}

// ─── 模型费率展示辅助 ──────────────────────────────────

// ModelPointInfo 是传递给前端 /api/v2/models 的费率摘要
type ModelPointInfo struct {
	PointsPerKToken float64 `json:"points_per_k_token"` // 综合"约X点/千字"
	CostLevel       string  `json:"cost_level"`         // economy | standard | premium
}

// GetModelPointInfo 获取模型的费率展示信息
func (pc *PointCalculator) GetModelPointInfo(ctx context.Context, modelName string) (*ModelPointInfo, error) {
	// 查询 writing 费率作为默认展示
	pointsPerK, err := pc.PointsPerKToken(ctx, modelName, "writing")
	if err != nil {
		return &ModelPointInfo{
			PointsPerKToken: 1.0,
			CostLevel:       "economy",
		}, nil
	}

	// 根据综合费率归类
	level := "economy"
	switch {
	case pointsPerK <= 1.5:
		level = "economy"
	case pointsPerK <= 3.0:
		level = "standard"
	default:
		level = "premium"
	}

	return &ModelPointInfo{
		PointsPerKToken: pointsPerK,
		CostLevel:       level,
	}, nil
}

// ─── 余额检查（供 billing middleware 使用）──────────────────

// CheckBalance 检查用户余额是否足够
// 返回余额和是否足够
func CheckBalance(ctx context.Context, db *sql.DB, userID string, required float64) (balance float64, ok bool, err error) {
	err = db.QueryRowContext(ctx, `
		SELECT COALESCE(balance, 0) FROM user_point_balance WHERE user_id = $1
	`, userID).Scan(&balance)
	if err == sql.ErrNoRows {
		return 0, false, nil // 用户没有余额记录
	}
	if err != nil {
		return 0, false, err
	}
	return balance, balance >= required, nil
}

// DeductPoints 扣减点数（原子操作）
// 返回扣减后的余额
func DeductPoints(ctx context.Context, db *sql.DB, userID string, points float64) (newBalance float64, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// 锁定行并检查余额
	var balance float64
	err = tx.QueryRowContext(ctx, `
		SELECT balance FROM user_point_balance WHERE user_id = $1 FOR UPDATE
	`, userID).Scan(&balance)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("user has no point balance record")
	}
	if err != nil {
		return 0, err
	}

	if balance < points {
		return balance, fmt.Errorf("insufficient balance: have %.2f, need %.2f", balance, points)
	}

	newBalance = balance - points
	_, err = tx.ExecContext(ctx, `
		UPDATE user_point_balance
		SET balance = $2, total_consumed = total_consumed + $3, updated_at = NOW()
		WHERE user_id = $1
	`, userID, newBalance, points)
	if err != nil {
		return 0, err
	}

	return newBalance, tx.Commit()
}

// RefundPoints 退还点数（预扣多余部分）
func RefundPoints(ctx context.Context, db *sql.DB, userID string, points float64) (newBalance float64, err error) {
	_, err = db.ExecContext(ctx, `
		UPDATE user_point_balance
		SET balance = balance + $2, total_refunded = total_refunded + $2, updated_at = NOW()
		WHERE user_id = $1
	`, userID, points)
	if err != nil {
		return 0, err
	}

	err = db.QueryRowContext(ctx, `
		SELECT balance FROM user_point_balance WHERE user_id = $1
	`, userID).Scan(&newBalance)
	return newBalance, err
}

// EnsureUserBalance 确保用户有余额记录（注册时调用）
func EnsureUserBalance(ctx context.Context, db *sql.DB, userID string, initialBalance float64) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO user_point_balance (user_id, balance, total_recharged)
		VALUES ($1, $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, initialBalance)
	return err
}

// AddPoints 充值点数
func AddPoints(ctx context.Context, db *sql.DB, userID string, points float64) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO user_point_balance (user_id, balance, total_recharged)
		VALUES ($1, $2, $2)
		ON CONFLICT (user_id) DO UPDATE SET
			balance = user_point_balance.balance + $2,
			total_recharged = user_point_balance.total_recharged + $2,
			updated_at = NOW()
	`, userID, points)
	return err
}
