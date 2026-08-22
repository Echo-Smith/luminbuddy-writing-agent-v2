package database

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// ─── Billing Repo: 计费数据访问层 ──────────────────────────
//
// 管理 point_consumption_log、recharge_orders、subscription_plans、
// user_subscriptions、user_point_balance 的数据库操作。
//
// 点数扣减/退还的原子操作在 services/point_calculator.go 中实现
//（因为需要配合事务锁），此处只做查询和记录。

// ─── 数据结构 ───────────────────────────────────────────

type PointBalance struct {
	UserID          string  `json:"user_id"`
	Balance         float64 `json:"balance"`
	TotalRecharged  float64 `json:"total_recharged"`
	TotalConsumed   float64 `json:"total_consumed"`
	TotalRefunded   float64 `json:"total_refunded"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ConsumptionLog struct {
	ID                string    `json:"id"`
	UserID            string    `json:"user_id"`
	TraceID           string    `json:"trace_id,omitempty"`
	TaskType          string    `json:"task_type"`
	ModelName         string    `json:"model_name"`
	PromptTokens      int       `json:"prompt_tokens"`
	CompletionTokens  int       `json:"completion_tokens"`
	InputRate         float64   `json:"input_rate"`
	OutputRate        float64   `json:"output_rate"`
	PointsUsed        float64   `json:"points_used"`
	BalanceBefore     float64   `json:"balance_before"`
	BalanceAfter      float64   `json:"balance_after"`
	CreatedAt         time.Time `json:"created_at"`
}

type RechargeOrder struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Amount        float64    `json:"amount"`
	PointAmount   float64    `json:"point_amount"`
	PaymentMethod string     `json:"payment_method"`
	PaymentURL    string     `json:"payment_url,omitempty"`
	Status        string     `json:"status"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type SubscriptionPlan struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	DisplayName  string  `json:"display_name"`
	PriceMonthly float64 `json:"price_monthly"`
	PointQuota   float64 `json:"point_quota"`
	Features     map[string]any `json:"features"`
	IsActive     bool    `json:"is_active"`
	IsPopular    bool    `json:"is_popular"`
	SortOrder    int     `json:"sort_order"`
}

type UserSubscription struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	PlanID     string     `json:"plan_id"`
	PlanName   string     `json:"plan_name"`
	PlanDisplayName string  `json:"plan_display_name"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	AutoRenew  bool       `json:"auto_renew"`
}

type PointRate struct {
	ID          string    `json:"id"`
	ModelName   string    `json:"model_name"`
	TaskType    string    `json:"task_type"`
	InputRate   float64   `json:"input_rate"`
	OutputRate  float64   `json:"output_rate"`
	IsActive    bool      `json:"is_active"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ─── BillingRepo ─────────────────────────────────────────

type BillingRepo struct {
	db *DB
}

func NewBillingRepo(db *DB) *BillingRepo {
	return &BillingRepo{db: db}
}

// DB returns the underlying database connection.
func (r *BillingRepo) DB() *DB {
	return r.db
}

// ─── 余额查询 ───────────────────────────────────────────

func (r *BillingRepo) GetBalance(ctx context.Context, userID string) (*PointBalance, error) {
	var b PointBalance
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id::text, balance, total_recharged, total_consumed, total_refunded, updated_at
		FROM user_point_balance WHERE user_id = $1::uuid
	`, userID).Scan(&b.UserID, &b.Balance, &b.TotalRecharged, &b.TotalConsumed, &b.TotalRefunded, &b.UpdatedAt)
	if err != nil {
		return &PointBalance{UserID: userID, Balance: 0}, nil // 没有记录 = 余额为 0
	}
	return &b, nil
}

// ─── 消费记录 ───────────────────────────────────────────

func (r *BillingRepo) InsertConsumption(ctx context.Context, log *ConsumptionLog) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO point_consumption_log (
			user_id, trace_id, task_type, model_name,
			prompt_tokens, completion_tokens, input_rate, output_rate,
			points_used, balance_before, balance_after, metadata
		) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, log.UserID, log.TraceID, log.TaskType, log.ModelName,
		log.PromptTokens, log.CompletionTokens, log.InputRate, log.OutputRate,
		log.PointsUsed, log.BalanceBefore, log.BalanceAfter, "{}")
	return err
}

func (r *BillingRepo) ListConsumption(ctx context.Context, userID string, days, limit, offset int) ([]*ConsumptionLog, int, error) {
	if days <= 0 {
		days = 30
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var total int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM point_consumption_log
		WHERE user_id = $1::uuid AND created_at >= NOW() - INTERVAL '%d days'
	`, userID).Scan(&total) // nolint:govet
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id::text, user_id::text, COALESCE(trace_id, ''), task_type, COALESCE(model_name, ''),
		       prompt_tokens, completion_tokens, input_rate, output_rate,
		       points_used, balance_before, balance_after, created_at
		FROM point_consumption_log
		WHERE user_id = $1::uuid AND created_at >= NOW() - INTERVAL '%d days'
		ORDER BY created_at DESC
		LIMIT %d OFFSET %d
	`, days, limit, offset), userID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []*ConsumptionLog
	for rows.Next() {
		var l ConsumptionLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.TraceID, &l.TaskType, &l.ModelName,
			&l.PromptTokens, &l.CompletionTokens, &l.InputRate, &l.OutputRate,
			&l.PointsUsed, &l.BalanceBefore, &l.BalanceAfter, &l.CreatedAt); err != nil {
			continue
		}
		logs = append(logs, &l)
	}
	return logs, total, nil
}

func (r *BillingRepo) GetConsumptionSummary(ctx context.Context, userID string, days int) (totalPoints float64, byCategory map[string]float64, err error) {
	if days <= 0 {
		days = 30
	}
	byCategory = make(map[string]float64)

	err = r.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(SUM(points_used), 0) FROM point_consumption_log
		WHERE user_id = $1::uuid AND created_at >= NOW() - INTERVAL '%d days'
	`, days), userID).Scan(&totalPoints)
	if err != nil {
		return 0, byCategory, err
	}

	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT task_type, COALESCE(SUM(points_used), 0)
		FROM point_consumption_log
		WHERE user_id = $1::uuid AND created_at >= NOW() - INTERVAL '%d days'
		GROUP BY task_type
	`, days), userID)
	if err != nil {
		return totalPoints, byCategory, nil
	}
	defer rows.Close()
	for rows.Next() {
		var cat string
		var pts float64
		if rows.Scan(&cat, &pts) == nil {
			byCategory[cat] = pts
		}
	}
	return totalPoints, byCategory, nil
}

// ─── 充值订单 ───────────────────────────────────────────

func (r *BillingRepo) CreateRechargeOrder(ctx context.Context, order *RechargeOrder) (*RechargeOrder, error) {
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO recharge_orders (user_id, amount, point_amount, payment_method, payment_url, status, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5, 'pending', NOW() + INTERVAL '30 minutes')
		RETURNING id::text, created_at, expires_at
	`, order.UserID, order.Amount, order.PointAmount, order.PaymentMethod, order.PaymentURL,
	).Scan(&order.ID, &order.CreatedAt, &order.ExpiresAt)
	if err != nil {
		return nil, err
	}
	order.Status = "pending"
	return order, nil
}

func (r *BillingRepo) ListRechargeOrders(ctx context.Context, userID string, limit int) ([]*RechargeOrder, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, user_id::text, amount, point_amount, payment_method,
		       COALESCE(payment_url, ''), status, paid_at, expires_at, created_at
		FROM recharge_orders
		WHERE user_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*RechargeOrder
	for rows.Next() {
		var o RechargeOrder
		if err := rows.Scan(&o.ID, &o.UserID, &o.Amount, &o.PointAmount, &o.PaymentMethod,
			&o.PaymentURL, &o.Status, &o.PaidAt, &o.ExpiresAt, &o.CreatedAt); err != nil {
			continue
		}
		orders = append(orders, &o)
	}
	return orders, nil
}

// ─── 套餐 ──────────────────────────────────────────────

func (r *BillingRepo) ListPlans(ctx context.Context) ([]*SubscriptionPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, name, display_name, price_monthly, point_quota, features, is_active, is_popular, sort_order
		FROM subscription_plans
		WHERE is_active = TRUE
		ORDER BY sort_order ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []*SubscriptionPlan
	for rows.Next() {
		var p SubscriptionPlan
		if err := rows.Scan(&p.ID, &p.Name, &p.DisplayName, &p.PriceMonthly, &p.PointQuota,
			&p.Features, &p.IsActive, &p.IsPopular, &p.SortOrder); err != nil {
			continue
		}
		plans = append(plans, &p)
	}
	return plans, nil
}

func (r *BillingRepo) GetPlan(ctx context.Context, planID string) (*SubscriptionPlan, error) {
	var p SubscriptionPlan
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, name, display_name, price_monthly, point_quota, features, is_active, is_popular, sort_order
		FROM subscription_plans WHERE id = $1::uuid
	`, planID).Scan(&p.ID, &p.Name, &p.DisplayName, &p.PriceMonthly, &p.PointQuota,
		&p.Features, &p.IsActive, &p.IsPopular, &p.SortOrder)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ─── 用户订阅 ───────────────────────────────────────────

func (r *BillingRepo) GetActiveSubscription(ctx context.Context, userID string) (*UserSubscription, error) {
	var s UserSubscription
	var planName, planDisplayName string
	err := r.db.QueryRowContext(ctx, `
		SELECT us.id::text, us.user_id::text, us.plan_id::text,
		       sp.name, sp.display_name,
		       us.status, us.started_at, us.expires_at, us.auto_renew
		FROM user_subscriptions us
		JOIN subscription_plans sp ON us.plan_id = sp.id
		WHERE us.user_id = $1::uuid AND us.status = 'active'
		ORDER BY us.created_at DESC LIMIT 1
	`, userID).Scan(&s.ID, &s.UserID, &s.PlanID, &planName, &planDisplayName,
		&s.Status, &s.StartedAt, &s.ExpiresAt, &s.AutoRenew)
	if err != nil {
		return nil, err
	}
	s.PlanName = planName
	s.PlanDisplayName = planDisplayName
	return &s, nil
}

func (r *BillingRepo) CreateSubscription(ctx context.Context, userID, planID string) (*UserSubscription, error) {
	// 获取套餐信息（含 point_quota）
	plan, err := r.GetPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 取消旧的活跃订阅
	_, _ = tx.ExecContext(ctx, `
		UPDATE user_subscriptions SET status = 'cancelled', cancelled_at = NOW()
		WHERE user_id = $1::uuid AND status = 'active'
	`, userID)

	var subID string
	var startedAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO user_subscriptions (user_id, plan_id, status, started_at, expires_at, auto_renew)
		VALUES ($1::uuid, $2::uuid, 'active', NOW(), NOW() + INTERVAL '30 days', false)
		RETURNING id::text, started_at
	`, userID, planID).Scan(&subID, &startedAt)
	if err != nil {
		return nil, err
	}

	// 给用户充值套餐对应的点数
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_point_balance (user_id, balance, total_recharged)
		VALUES ($1::uuid, $2, $2)
		ON CONFLICT (user_id) DO UPDATE SET
			balance = user_point_balance.balance + $2,
			total_recharged = user_point_balance.total_recharged + $2,
			updated_at = NOW()
	`, userID, plan.PointQuota)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &UserSubscription{
		ID:        subID,
		UserID:    userID,
		PlanID:    planID,
		PlanName:  plan.Name,
		PlanDisplayName: plan.DisplayName,
		Status:    "active",
		StartedAt: startedAt,
		AutoRenew: false,
	}, nil
}

// ─── 费率管理（Admin）────────────────────────────────────

func (r *BillingRepo) ListPointRates(ctx context.Context) ([]*PointRate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, model_name, task_type, input_rate, output_rate, is_active, updated_at
		FROM point_rates
		ORDER BY model_name, task_type
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rates []*PointRate
	for rows.Next() {
		var pr PointRate
		if err := rows.Scan(&pr.ID, &pr.ModelName, &pr.TaskType, &pr.InputRate, &pr.OutputRate, &pr.IsActive, &pr.UpdatedAt); err != nil {
			continue
		}
		rates = append(rates, &pr)
	}
	return rates, nil
}

func (r *BillingRepo) UpdatePointRate(ctx context.Context, rateID string, inputRate, outputRate float64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE point_rates SET input_rate = $2, output_rate = $3, updated_at = NOW()
		WHERE id = $1::uuid
	`, rateID, inputRate, outputRate)
	return err
}

func (r *BillingRepo) CreatePointRate(ctx context.Context, modelName, taskType string, inputRate, outputRate float64) (*PointRate, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO point_rates (model_name, task_type, input_rate, output_rate)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (model_name, task_type) DO UPDATE SET
			input_rate = EXCLUDED.input_rate,
			output_rate = EXCLUDED.output_rate,
			updated_at = NOW()
		RETURNING id::text
	`, modelName, taskType, inputRate, outputRate).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &PointRate{ID: id, ModelName: modelName, TaskType: taskType, InputRate: inputRate, OutputRate: outputRate, IsActive: true}, nil
}

func (r *BillingRepo) DeletePointRate(ctx context.Context, rateID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM point_rates WHERE id = $1::uuid`, rateID)
	return err
}

func (r *BillingRepo) GetGlobalMultiplier(ctx context.Context) (float64, error) {
	var m float64
	err := r.db.QueryRowContext(ctx, `SELECT global_multiplier FROM billing_config WHERE id = 1`).Scan(&m)
	if err != nil {
		return 1.0, nil
	}
	return m, nil
}

func (r *BillingRepo) SetGlobalMultiplier(ctx context.Context, multiplier float64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE billing_config SET global_multiplier = $2, updated_at = NOW() WHERE id = 1
	`, multiplier)
	return err
}

// ─── Admin 统计 ─────────────────────────────────────────

type BillingOverview struct {
	TotalRevenue        float64 `json:"total_revenue"`
	TotalPointsSold     float64 `json:"total_points_sold"`
	TotalPointsConsumed float64 `json:"total_points_consumed"`
	ActiveSubscriptions int     `json:"active_subscriptions"`
	TotalRechargeOrders int     `json:"total_recharge_orders"`
}

func (r *BillingRepo) GetBillingOverview(ctx context.Context) (*BillingOverview, error) {
	o := &BillingOverview{}

	r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0) FROM recharge_orders WHERE status = 'paid'
	`).Scan(&o.TotalRevenue)

	r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(point_amount), 0) FROM recharge_orders WHERE status = 'paid'
	`).Scan(&o.TotalPointsSold)

	r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(points_used), 0) FROM point_consumption_log
	`).Scan(&o.TotalPointsConsumed)

	r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_subscriptions WHERE status = 'active'
	`).Scan(&o.ActiveSubscriptions)

	r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM recharge_orders
	`).Scan(&o.TotalRechargeOrders)

	return o, nil
}

type UserBillingInfo struct {
	UserID         string  `json:"user_id"`
	Username       string  `json:"username"`
	PlanName       string  `json:"plan_name"`
	PlanDisplayName string `json:"plan_display_name"`
	Balance        float64 `json:"balance"`
	TotalConsumed  float64 `json:"total_consumed"`
	LastActive     *time.Time `json:"last_active,omitempty"`
}

func (r *BillingRepo) ListUserBilling(ctx context.Context, limit, offset int) ([]*UserBillingInfo, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT u.id::text, u.name,
		       COALESCE(sp.name, 'free') AS plan_name,
		       COALESCE(sp.display_name, '免费版') AS plan_display_name,
		       COALESCE(b.balance, 0) AS balance,
		       COALESCE(b.total_consumed, 0) AS total_consumed,
		       (SELECT MAX(created_at) FROM point_consumption_log WHERE user_id = u.id) AS last_active
		FROM users u
		LEFT JOIN user_subscriptions us ON u.id = us.user_id AND us.status = 'active'
		LEFT JOIN subscription_plans sp ON us.plan_id = sp.id
		LEFT JOIN user_point_balance b ON u.id = b.user_id
		ORDER BY COALESCE(b.total_consumed, 0) DESC
		LIMIT %d OFFSET %d
	`, limit, offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*UserBillingInfo
	for rows.Next() {
		var u UserBillingInfo
		if err := rows.Scan(&u.UserID, &u.Username, &u.PlanName, &u.PlanDisplayName,
			&u.Balance, &u.TotalConsumed, &u.LastActive); err != nil {
			continue
		}
		users = append(users, &u)
	}
	return users, nil
}

// ─── 用户注册时初始化余额 ───────────────────────────────

func (r *BillingRepo) InitUserBalance(ctx context.Context, userID string, initialBalance float64) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_point_balance (user_id, balance, total_recharged)
		VALUES ($1::uuid, $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, initialBalance)
	return err
}

// ─── 手动充值（Admin）────────────────────────────────────

func (r *BillingRepo) AdminRecharge(ctx context.Context, userID string, points float64, adminID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 充值点数
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_point_balance (user_id, balance, total_recharged)
		VALUES ($1::uuid, $2, $2)
		ON CONFLICT (user_id) DO UPDATE SET
			balance = user_point_balance.balance + $2,
			total_recharged = user_point_balance.total_recharged + $2,
			updated_at = NOW()
	`, userID, points)
	if err != nil {
		return err
	}

	// 创建充值订单（manual 类型）
	_, err = tx.ExecContext(ctx, `
		INSERT INTO recharge_orders (user_id, amount, point_amount, payment_method, status, paid_at)
		VALUES ($1::uuid, 0, $2, 'manual', 'paid', NOW())
	`, userID, points)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// ─── 兑换码 ─────────────────────────────────────────────

// RedeemCode 表示一条兑换码记录
type RedeemCode struct {
	ID           string     `json:"id"`
	Code         string     `json:"code"`
	PointAmount  float64    `json:"point_amount"`
	BatchID      string     `json:"batch_id,omitempty"`
	BatchLabel   string     `json:"batch_label,omitempty"`
	Status       string     `json:"status"` // unused | used | disabled | expired
	CreatedBy    string     `json:"created_by,omitempty"`
	RedeemedBy   string     `json:"redeemed_by,omitempty"`
	RedeemedAt   *time.Time `json:"redeemed_at,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// 兑换码字符集：去掉 0/O/1/I/L 等易混淆字符
const redeemCodeCharset = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func generateRedeemCode(length int) string {
	b := make([]byte, length)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(redeemCodeCharset))))
		b[i] = redeemCodeCharset[n.Int64()]
	}
	return string(b)
}

// CreateRedeemCodes 批量生成兑换码（事务内完成）
func (r *BillingRepo) CreateRedeemCodes(ctx context.Context, count int, pointAmount float64, batchLabel string, expiresAt *time.Time, adminID string) ([]*RedeemCode, error) {
	if count <= 0 || count > 1000 {
		count = 1
	}
	if count > 500 {
		count = 500 // 单次上限 500 条
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	codes := make([]*RedeemCode, 0, count)
	for i := 0; i < count; i++ {
		// 生成 12 位兑换码，格式 XXXX-XXXX-XXXX
		raw := generateRedeemCode(12)
		code := raw[:4] + "-" + raw[4:8] + "-" + raw[8:]

		var id string
		var createdAt time.Time
		err := tx.QueryRowContext(ctx, `
			INSERT INTO redeem_codes (code, point_amount, batch_label, status, created_by, expires_at)
			VALUES ($1, $2, $3, 'unused', $4::uuid, $5)
			RETURNING id::text, created_at
		`, code, pointAmount, batchLabel, adminID, expiresAt).Scan(&id, &createdAt)
		if err != nil {
			// 唯一冲突，重试一次
			raw2 := generateRedeemCode(12)
			code2 := raw2[:4] + "-" + raw2[4:8] + "-" + raw2[8:]
			err = tx.QueryRowContext(ctx, `
				INSERT INTO redeem_codes (code, point_amount, batch_label, status, created_by, expires_at)
				VALUES ($1, $2, $3, 'unused', $4::uuid, $5)
				RETURNING id::text, created_at
			`, code2, pointAmount, batchLabel, adminID, expiresAt).Scan(&id, &createdAt)
			if err != nil {
				return nil, fmt.Errorf("failed to generate unique code after retry: %w", err)
			}
			code = code2
		}

		codes = append(codes, &RedeemCode{
			ID:          id,
			Code:        code,
			PointAmount: pointAmount,
			BatchLabel:  batchLabel,
			Status:      "unused",
			ExpiresAt:   expiresAt,
			CreatedAt:   createdAt,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}

// RedeemCode 兑换码核销（原子操作：锁定行 → 检查状态 → 更新 → 加积分）
// 返回兑换的积分数量
func (r *BillingRepo) RedeemCode(ctx context.Context, code string, userID string) (float64, error) {
	code = strings.ToUpper(strings.TrimSpace(code))

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// 锁定兑换码行
	var rc RedeemCode
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, code, point_amount, status, expires_at
		FROM redeem_codes
		WHERE code = $1
		FOR UPDATE
	`, code).Scan(&rc.ID, &rc.Code, &rc.PointAmount, &rc.Status, &rc.ExpiresAt)
	if err != nil {
		return 0, fmt.Errorf("兑换码不存在")
	}

	// 检查状态
	if rc.Status != "unused" {
		if rc.Status == "used" {
			return 0, fmt.Errorf("兑换码已被使用")
		}
		if rc.Status == "disabled" {
			return 0, fmt.Errorf("兑换码已作废")
		}
		return 0, fmt.Errorf("兑换码状态异常: %s", rc.Status)
	}

	// 检查过期
	if rc.ExpiresAt != nil && time.Now().After(*rc.ExpiresAt) {
		_, _ = tx.ExecContext(ctx, `UPDATE redeem_codes SET status = 'expired' WHERE id = $1::uuid`, rc.ID)
		_ = tx.Commit()
		return 0, fmt.Errorf("兑换码已过期")
	}

	// 标记为已使用
	_, err = tx.ExecContext(ctx, `
		UPDATE redeem_codes
		SET status = 'used', redeemed_by = $2::uuid, redeemed_at = NOW()
		WHERE id = $1::uuid
	`, rc.ID, userID)
	if err != nil {
		return 0, err
	}

	// 给用户加积分
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_point_balance (user_id, balance, total_recharged)
		VALUES ($1::uuid, $2, $2)
		ON CONFLICT (user_id) DO UPDATE SET
			balance = user_point_balance.balance + $2,
			total_recharged = user_point_balance.total_recharged + $2,
			updated_at = NOW()
	`, userID, rc.PointAmount)
	if err != nil {
		return 0, err
	}

	// 记录充值订单（兑换码类型）
	_, err = tx.ExecContext(ctx, `
		INSERT INTO recharge_orders (user_id, amount, point_amount, payment_method, status, paid_at)
		VALUES ($1::uuid, 0, $2, 'redeem_code', 'paid', NOW())
	`, userID, rc.PointAmount)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return rc.PointAmount, nil
}

// ListRedeemCodes 分页查询兑换码
func (r *BillingRepo) ListRedeemCodes(ctx context.Context, status string, limit, offset int) ([]*RedeemCode, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var total int
	countQuery := `SELECT COUNT(*) FROM redeem_codes`
	if status != "" {
		countQuery += ` WHERE status = $1`
	}
	args := []interface{}{}
	if status != "" {
		args = append(args, status)
	}
	_ = r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total)

	listQuery := `
		SELECT id::text, code, point_amount, COALESCE(batch_label, ''),
		       status, COALESCE(redeemed_by::text, ''), redeemed_at, expires_at, created_at
		FROM redeem_codes
	`
	args2 := []interface{}{}
	if status != "" {
		listQuery += ` WHERE status = $1`
		args2 = append(args2, status)
	}
	listQuery += fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d OFFSET %d`, limit, offset)

	rows, err := r.db.QueryContext(ctx, listQuery, args2...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var codes []*RedeemCode
	for rows.Next() {
		var rc RedeemCode
		if err := rows.Scan(&rc.ID, &rc.Code, &rc.PointAmount, &rc.BatchLabel,
			&rc.Status, &rc.RedeemedBy, &rc.RedeemedAt, &rc.ExpiresAt, &rc.CreatedAt); err != nil {
			continue
		}
		codes = append(codes, &rc)
	}
	return codes, total, nil
}

// DisableRedeemCode 作废兑换码
func (r *BillingRepo) DisableRedeemCode(ctx context.Context, codeID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE redeem_codes SET status = 'disabled'
		WHERE id = $1::uuid AND status = 'unused'
	`, codeID)
	return err
}
