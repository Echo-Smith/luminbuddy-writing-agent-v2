package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── 用户侧 Billing API ──────────────────────────────────
//
// 所有接口需要 JWT 认证，从 context 中提取 user_id。
// 路由前缀：/api/v2/billing/*

// handleBillingBalance 获取用户点数余额和当前套餐
// GET /api/v2/billing/balance
func (s *Server) handleBillingBalance(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}

	result := map[string]interface{}{
		"point_balance": float64(0),
		"plan_name":     "free",
		"plan_display_name": "免费版",
		"plan_expires_at":   nil,
		"features":      map[string]any{},
	}

	if s.billingRepo != nil {
		balance, err := s.billingRepo.GetBalance(r.Context(), user.Sub)
		if err == nil && balance != nil {
			result["point_balance"] = balance.Balance
			result["total_recharged"] = balance.TotalRecharged
			result["total_consumed"] = balance.TotalConsumed
		}

		sub, err := s.billingRepo.GetActiveSubscription(r.Context(), user.Sub)
		if err == nil && sub != nil {
			result["plan_name"] = sub.PlanName
			result["plan_display_name"] = sub.PlanDisplayName
			if sub.ExpiresAt != nil {
				result["plan_expires_at"] = sub.ExpiresAt
			}
			// 获取套餐 features
			if plan, err := s.billingRepo.GetPlan(r.Context(), sub.PlanID); err == nil && plan != nil {
				result["features"] = plan.Features
			}
		}
	}

	response.OK(w, result)
}

// handleBillingPlans 获取可用套餐列表
// GET /api/v2/billing/plans
func (s *Server) handleBillingPlans(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{"plans": []interface{}{}})
		return
	}

	plans, err := s.billingRepo.ListPlans(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list plans")
		return
	}
	response.OK(w, map[string]interface{}{"plans": plans})
}

// handleBillingSubscribe 订阅套餐
// POST /api/v2/billing/subscribe
func (s *Server) handleBillingSubscribe(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	var body struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.PlanID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "plan_id is required")
		return
	}

	sub, err := s.billingRepo.CreateSubscription(r.Context(), user.Sub, body.PlanID)
	if err != nil {
		slog.Warn("billing: subscribe failed", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "subscribe_failed", "failed to create subscription")
		return
	}

	response.OK(w, sub)
}

// handleBillingSubscription 获取当前订阅
// GET /api/v2/billing/subscription
func (s *Server) handleBillingSubscription(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{})
		return
	}

	sub, err := s.billingRepo.GetActiveSubscription(r.Context(), user.Sub)
	if err != nil || sub == nil {
		response.OK(w, map[string]interface{}{
			"status": "none",
			"plan_name": "free",
		})
		return
	}
	response.OK(w, sub)
}

// handleBillingConsumption 获取消费记录
// GET /api/v2/billing/consumption?days=30&page=1&limit=20
func (s *Server) handleBillingConsumption(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{"items": []interface{}{}, "total": 0})
		return
	}

	days := parseIntDefault(r.URL.Query().Get("days"), 30)
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	limit := parseIntDefault(r.URL.Query().Get("limit"), 20)
	offset := (page - 1) * limit

	logs, total, err := s.billingRepo.ListConsumption(r.Context(), user.Sub, days, limit, offset)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list consumption")
		return
	}

	response.OK(w, map[string]interface{}{
		"items":      logs,
		"total":      total,
		"page":       page,
		"limit":      limit,
		"days":       days,
	})
}

// handleBillingConsumptionSummary 获取消费汇总
// GET /api/v2/billing/consumption/summary?days=30
func (s *Server) handleBillingConsumptionSummary(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{"total_consumed": 0, "by_category": map[string]float64{}})
		return
	}

	days := parseIntDefault(r.URL.Query().Get("days"), 30)
	total, byCategory, err := s.billingRepo.GetConsumptionSummary(r.Context(), user.Sub, days)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get summary")
		return
	}

	response.OK(w, map[string]interface{}{
		"total_consumed": total,
		"by_category":    byCategory,
		"days":           days,
	})
}

// handleBillingRecharge 创建充值订单
// POST /api/v2/billing/recharge
func (s *Server) handleBillingRecharge(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	var body struct {
		PointAmount   float64 `json:"point_amount"`
		PaymentMethod string  `json:"payment_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.PointAmount <= 0 {
		response.Err(w, http.StatusBadRequest, "bad_request", "point_amount must be positive")
		return
	}
	if body.PaymentMethod == "" {
		body.PaymentMethod = "manual"
	}

	// 计算支付金额（1 点 = ¥0.1）
	amount := body.PointAmount * 0.1

	order := &database.RechargeOrder{
		UserID:        user.Sub,
		Amount:        amount,
		PointAmount:   body.PointAmount,
		PaymentMethod: body.PaymentMethod,
	}

	order, err := s.billingRepo.CreateRechargeOrder(r.Context(), order)
	if err != nil {
		slog.Warn("billing: create recharge order failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "order_failed", "failed to create order")
		return
	}

	response.OK(w, order)
}

// handleBillingRechargeOrders 获取充值订单列表
// GET /api/v2/billing/recharge/orders
func (s *Server) handleBillingRechargeOrders(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{"orders": []interface{}{}})
		return
	}

	limit := parseIntDefault(r.URL.Query().Get("limit"), 20)
	orders, err := s.billingRepo.ListRechargeOrders(r.Context(), user.Sub, limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list orders")
		return
	}

	response.OK(w, map[string]interface{}{"orders": orders})
}

// handleBillingFeatures 获取用户功能权限
// GET /api/v2/billing/features
func (s *Server) handleBillingFeatures(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}

	// 默认免费版权限
	features := map[string]any{
		"editorial_mode":    false,
		"max_agents":        1,
		"memory_enabled":   false,
		"custom_styles":     false,
		"kb_doc_limit":      0,
		"max_word_count":    800,
		"daily_write_limit": 3,
	}

	if s.billingRepo != nil {
		sub, err := s.billingRepo.GetActiveSubscription(r.Context(), user.Sub)
		if err == nil && sub != nil {
			if plan, err := s.billingRepo.GetPlan(r.Context(), sub.PlanID); err == nil && plan != nil {
				// 合并套餐 features
				for k, v := range plan.Features {
					features[k] = v
				}
			}
		}
	}

	response.OK(w, features)
}

// ─── 计费扣减（在写作完成时调用）──────────────────────────

// SettleWritingPoints 结算一次写作的点数消耗
// 在 harness.Run() / dag_executor 完成后调用
func (s *Server) SettleWritingPoints(ctx context.Context, userID, traceID, modelName, taskType string, promptTokens, completionTokens int) (pointsUsed float64, err error) {
	if s.billingRepo == nil || s.pointCalc == nil {
		return 0, nil
	}

	if userID == "" || userID == "anonymous" {
		return 0, nil
	}

	// Admin 用户不消耗积分
	if userID == AdminUserID {
		return 0, nil
	}

	// 1. 计算点数
	result, err := s.pointCalc.Calculate(ctx, promptTokens, completionTokens, modelName, taskType)
	if err != nil {
		return 0, fmt.Errorf("point calculation failed: %w", err)
	}
	pointsUsed = result.Points

	if pointsUsed <= 0 {
		return 0, nil
	}

	// 2. 获取当前余额（记录 balance_before）
	balance, err := s.billingRepo.GetBalance(ctx, userID)
	if err != nil {
		return 0, err
	}
	balanceBefore := balance.Balance

	// 3. 扣减点数
	newBalance, err := services.DeductPoints(ctx, s.db.DB, userID, pointsUsed)
	if err != nil {
		slog.Warn("billing: deduct points failed",
			"error", err, "user_id", userID, "points", pointsUsed, "balance", balanceBefore)
		// 余额不足不阻塞写作（已有预扣逻辑），只记录日志
		return pointsUsed, nil
	}

	// 4. 记录消费明细
	log := &database.ConsumptionLog{
		UserID:           userID,
		TraceID:          traceID,
		TaskType:         taskType,
		ModelName:        modelName,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		InputRate:        result.InputRate,
		OutputRate:       result.OutputRate,
		PointsUsed:       pointsUsed,
		BalanceBefore:    balanceBefore,
		BalanceAfter:     newBalance,
	}
	if err := s.billingRepo.InsertConsumption(ctx, log); err != nil {
		slog.Warn("billing: insert consumption log failed", "error", err)
	}

	slog.Info("billing: points settled",
		"user_id", userID,
		"trace_id", traceID,
		"task_type", taskType,
		"model", modelName,
		"prompt_tokens", promptTokens,
		"completion_tokens", completionTokens,
		"points_used", pointsUsed,
		"balance_before", balanceBefore,
		"balance_after", newBalance,
	)

	return pointsUsed, nil
}

// ─── 余额检查（在写作请求开始时调用）──────────────────────

// CheckUserBalance 检查用户余额是否足够
// minRequired 是预估最低消耗（0 表示不限制，只要有余额即可）
func (s *Server) CheckUserBalance(ctx context.Context, userID string, minRequired float64) (balance float64, ok bool, err error) {
	if s.billingRepo == nil {
		return 999999, true, nil // 计费系统未启用，允许通过
	}
	if userID == "" || userID == "anonymous" {
		return 999999, true, nil // 匿名用户不限制
	}
	if userID == AdminUserID {
		return 999999, true, nil // Admin 不限制
	}

	b, err := s.billingRepo.GetBalance(ctx, userID)
	if err != nil {
		return 0, false, err
	}

	if minRequired <= 0 {
		return b.Balance, b.Balance > 0, nil
	}
	return b.Balance, b.Balance >= minRequired, nil
}

// ─── 用户注册时初始化余额 ────────────────────────────────

// InitNewUserBalance 新用户注册时赠送初始点数
func (s *Server) InitNewUserBalance(ctx context.Context, userID string) {
	if s.billingRepo == nil {
		return
	}
	// 赠送 500 点（免费套餐额度）
	if err := s.billingRepo.InitUserBalance(ctx, userID, 500); err != nil {
		slog.Warn("billing: init user balance failed", "error", err, "user_id", userID)
	}
}

// ─── 兑换码兑换 ───────────────────────────────────────────

// handleBillingRedeem 用户兑换码兑换
// POST /api/v2/billing/redeem
func (s *Server) handleBillingRedeem(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.Code == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "code is required")
		return
	}

	points, err := s.billingRepo.RedeemCode(r.Context(), body.Code, user.Sub)
	if err != nil {
		slog.Warn("billing: redeem failed", "code", body.Code, "user_id", user.Sub, "error", err)
		response.Err(w, http.StatusBadRequest, "redeem_failed", err.Error())
		return
	}

	slog.Info("billing: redeem success", "user_id", user.Sub, "points", points)

	response.OK(w, map[string]interface{}{
		"points":      points,
		"message":     fmt.Sprintf("兑换成功，获得 %.0f 积分", points),
	})
}

// ─── DB 安全获取（避免 nil panic）─────────────────────────

var _ = context.Background
