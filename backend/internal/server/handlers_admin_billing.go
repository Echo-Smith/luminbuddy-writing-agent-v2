package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Admin Billing API ────────────────────────────────────
//
// 管理后台计费相关接口。
// 路由前缀：/api/v2/admin/billing/*
// 需要 admin token 认证。

// handleAdminBillingOverview 计费概览
// GET /api/v2/admin/billing/overview
func (s *Server) handleAdminBillingOverview(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{
			"total_revenue":         0,
			"total_points_sold":     0,
			"total_points_consumed": 0,
			"active_subscriptions":  0,
			"total_recharge_orders": 0,
		})
		return
	}

	overview, err := s.billingRepo.GetBillingOverview(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get overview")
		return
	}
	response.OK(w, overview)
}

// handleAdminBillingUsers 用户计费列表
// GET /api/v2/admin/billing/users?page=1&limit=20
func (s *Server) handleAdminBillingUsers(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{"users": []interface{}{}})
		return
	}

	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	limit := parseIntDefault(r.URL.Query().Get("limit"), 20)
	offset := (page - 1) * limit

	users, err := s.billingRepo.ListUserBilling(r.Context(), limit, offset)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list users")
		return
	}

	response.OK(w, map[string]interface{}{
		"users": users,
		"page":  page,
		"limit": limit,
	})
}

// handleAdminBillingUserDetail 用户计费详情
// GET /api/v2/admin/billing/users/:userId
func (s *Server) handleAdminBillingUserDetail(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	userID := r.URL.Query().Get("userId")
	if userID == "" {
		// 从路径中提取（chi 风格）
		userID = r.PathValue("userId")
	}
	if userID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "userId is required")
		return
	}

	balance, _ := s.billingRepo.GetBalance(r.Context(), userID)
	sub, _ := s.billingRepo.GetActiveSubscription(r.Context(), userID)
	orders, _ := s.billingRepo.ListRechargeOrders(r.Context(), userID, 10)
	logs, _, _ := s.billingRepo.ListConsumption(r.Context(), userID, 30, 10, 0)

	response.OK(w, map[string]interface{}{
		"balance":           balance,
		"subscription":      sub,
		"recent_orders":     orders,
		"recent_consumption": logs,
	})
}

// handleAdminBillingRevenue 收入统计
// GET /api/v2/admin/billing/revenue?days=30
func (s *Server) handleAdminBillingRevenue(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{
			"total_revenue": 0,
			"total_points_sold": 0,
		})
		return
	}

	days := parseIntDefault(r.URL.Query().Get("days"), 30)
	overview, err := s.billingRepo.GetBillingOverview(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get revenue")
		return
	}

	response.OK(w, map[string]interface{}{
		"total_revenue":         overview.TotalRevenue,
		"total_points_sold":     overview.TotalPointsSold,
		"total_points_consumed": overview.TotalPointsConsumed,
		"active_subscriptions":  overview.ActiveSubscriptions,
		"days":                  days,
	})
}

// ─── 费率管理 ───────────────────────────────────────────

// handleAdminBillingPointRates 获取费率列表
// GET /api/v2/admin/billing/point-rates
func (s *Server) handleAdminBillingPointRates(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{"rates": []interface{}{}})
		return
	}

	rates, err := s.billingRepo.ListPointRates(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list rates")
		return
	}

	multiplier := 1.0
	if m, err := s.billingRepo.GetGlobalMultiplier(r.Context()); err == nil {
		multiplier = m
	}

	response.OK(w, map[string]interface{}{
		"rates":            rates,
		"global_multiplier": multiplier,
	})
}

// handleAdminBillingUpdatePointRate 更新费率
// PUT /api/v2/admin/billing/point-rates/:id
func (s *Server) handleAdminBillingUpdatePointRate(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	rateID := r.PathValue("id")
	if rateID == "" {
		// fallback to query param
		rateID = r.URL.Query().Get("id")
	}
	if rateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "id is required")
		return
	}

	var body struct {
		InputRate  float64 `json:"input_rate"`
		OutputRate float64 `json:"output_rate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if body.InputRate < 0 || body.OutputRate < 0 {
		response.Err(w, http.StatusBadRequest, "bad_request", "rates must be non-negative")
		return
	}

	if err := s.billingRepo.UpdatePointRate(r.Context(), rateID, body.InputRate, body.OutputRate); err != nil {
		response.Err(w, http.StatusInternalServerError, "update_failed", "failed to update rate")
		return
	}

	// 清除费率缓存
	if s.pointCalc != nil {
		s.pointCalc.InvalidateCache()
	}

	slog.Info("admin: point rate updated", "rate_id", rateID, "input", body.InputRate, "output", body.OutputRate)
	response.OK(w, map[string]interface{}{"ok": true})
}

// handleAdminBillingCreatePointRate 创建费率
// POST /api/v2/admin/billing/point-rates
func (s *Server) handleAdminBillingCreatePointRate(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	var body struct {
		ModelName  string  `json:"model_name"`
		TaskType   string  `json:"task_type"`
		InputRate  float64 `json:"input_rate"`
		OutputRate float64 `json:"output_rate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.ModelName == "" || body.TaskType == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "model_name and task_type are required")
		return
	}

	rate, err := s.billingRepo.CreatePointRate(r.Context(), body.ModelName, body.TaskType, body.InputRate, body.OutputRate)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "create_failed", "failed to create rate")
		return
	}

	if s.pointCalc != nil {
		s.pointCalc.InvalidateCache()
	}

	response.Created(w, rate)
}

// handleAdminBillingDeletePointRate 删除费率
// DELETE /api/v2/admin/billing/point-rates/:id
func (s *Server) handleAdminBillingDeletePointRate(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	rateID := r.PathValue("id")
	if rateID == "" {
		rateID = r.URL.Query().Get("id")
	}
	if rateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "id is required")
		return
	}

	if err := s.billingRepo.DeletePointRate(r.Context(), rateID); err != nil {
		response.Err(w, http.StatusInternalServerError, "delete_failed", "failed to delete rate")
		return
	}

	if s.pointCalc != nil {
		s.pointCalc.InvalidateCache()
	}

	response.OK(w, map[string]interface{}{"ok": true})
}

// handleAdminBillingSetMultiplier 设置全局倍率
// PUT /api/v2/admin/billing/multiplier
func (s *Server) handleAdminBillingSetMultiplier(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	var body struct {
		Multiplier float64 `json:"multiplier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.Multiplier <= 0 || body.Multiplier > 10 {
		response.Err(w, http.StatusBadRequest, "bad_request", "multiplier must be between 0 and 10")
		return
	}

	if err := s.billingRepo.SetGlobalMultiplier(r.Context(), body.Multiplier); err != nil {
		response.Err(w, http.StatusInternalServerError, "update_failed", "failed to set multiplier")
		return
	}

	if s.pointCalc != nil {
		s.pointCalc.InvalidateCache()
	}

	slog.Info("admin: global multiplier set", "multiplier", body.Multiplier)
	response.OK(w, map[string]interface{}{"ok": true, "multiplier": body.Multiplier})
}

// ─── 套餐管理 ───────────────────────────────────────────

// handleAdminBillingPlans 获取套餐列表（含非活跃）
// GET /api/v2/admin/billing/plans
func (s *Server) handleAdminBillingPlans(w http.ResponseWriter, r *http.Request) {
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

// handleAdminBillingUpdatePlan 更新套餐
// PUT /api/v2/admin/billing/plans/:id
func (s *Server) handleAdminBillingUpdatePlan(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	planID := r.PathValue("id")
	if planID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "id is required")
		return
	}

	var body struct {
		PriceMonthly float64        `json:"price_monthly"`
		PointQuota   float64        `json:"point_quota"`
		Features     map[string]any `json:"features"`
		IsActive     *bool          `json:"is_active"`
		IsPopular    *bool          `json:"is_popular"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// 构建动态更新
	setParts := ""
	args := []interface{}{}
	argIdx := 1

	if body.PriceMonthly >= 0 {
		setParts += fmt.Sprintf("price_monthly = $%d, ", argIdx)
		args = append(args, body.PriceMonthly)
		argIdx++
	}
	if body.PointQuota >= 0 {
		setParts += fmt.Sprintf("point_quota = $%d, ", argIdx)
		args = append(args, body.PointQuota)
		argIdx++
	}
	if body.Features != nil {
		featuresJSON, _ := json.Marshal(body.Features)
		setParts += fmt.Sprintf("features = $%d, ", argIdx)
		args = append(args, string(featuresJSON))
		argIdx++
	}
	if body.IsActive != nil {
		setParts += fmt.Sprintf("is_active = $%d, ", argIdx)
		args = append(args, *body.IsActive)
		argIdx++
	}
	if body.IsPopular != nil {
		setParts += fmt.Sprintf("is_popular = $%d, ", argIdx)
		args = append(args, *body.IsPopular)
		argIdx++
	}

	if setParts == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "no fields to update")
		return
	}

	// 去掉末尾的 ", "
	setParts = setParts[:len(setParts)-2]
	setParts += fmt.Sprintf(", updated_at = NOW() WHERE id = $%d::uuid", argIdx)
	args = append(args, planID)

	_, err := s.billingRepo.DB().ExecContext(r.Context(),
		fmt.Sprintf("UPDATE subscription_plans SET %s", setParts),
		args...)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "update_failed", "failed to update plan")
		return
	}

	response.OK(w, map[string]interface{}{"ok": true})
}

// ─── 手动充值 ───────────────────────────────────────────

// handleAdminBillingRecharge 手动给用户充值点数
// POST /api/v2/admin/billing/recharge
func (s *Server) handleAdminBillingRecharge(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	var body struct {
		UserID  string  `json:"user_id"`
		Points  float64 `json:"points"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.UserID == "" || body.Points <= 0 {
		response.Err(w, http.StatusBadRequest, "bad_request", "user_id and positive points are required")
		return
	}

	admin := userFromContext(r.Context())
	adminID := "admin"
	if admin != nil {
		adminID = admin.Sub
	}

	if err := s.billingRepo.AdminRecharge(r.Context(), body.UserID, body.Points, adminID); err != nil {
		slog.Warn("admin: manual recharge failed", "error", err, "user_id", body.UserID)
		response.Err(w, http.StatusInternalServerError, "recharge_failed", "failed to recharge points")
		return
	}

	slog.Info("admin: manual recharge", "user_id", body.UserID, "points", body.Points, "admin", adminID)
	response.OK(w, map[string]interface{}{"ok": true, "points_added": body.Points})
}

// ─── 消费统计 ───────────────────────────────────────────

// handleAdminBillingConsumption 全局消费统计
// GET /api/v2/admin/billing/consumption?days=30
func (s *Server) handleAdminBillingConsumption(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{
			"total_consumed": 0,
			"by_category":    map[string]float64{},
		})
		return
	}

	days := parseIntDefault(r.URL.Query().Get("days"), 30)

	// 全局消费汇总
	var totalConsumed float64
	s.db.QueryRowContext(r.Context(), fmt.Sprintf(`
		SELECT COALESCE(SUM(points_used), 0) FROM point_consumption_log
		WHERE created_at >= NOW() - INTERVAL '%d days'
	`, days)).Scan(&totalConsumed)

	// 按操作类型分类
	rows, err := s.db.QueryContext(r.Context(), fmt.Sprintf(`
		SELECT task_type, COALESCE(SUM(points_used), 0)
		FROM point_consumption_log
		WHERE created_at >= NOW() - INTERVAL '%d days'
		GROUP BY task_type ORDER BY SUM(points_used) DESC
	`, days))
	if err != nil {
		response.OK(w, map[string]interface{}{
			"total_consumed": totalConsumed,
			"by_category":    map[string]float64{},
			"days":           days,
		})
		return
	}
	defer rows.Close()

	byCategory := make(map[string]float64)
	for rows.Next() {
		var cat string
		var pts float64
		if rows.Scan(&cat, &pts) == nil {
			byCategory[cat] = pts
		}
	}

	response.OK(w, map[string]interface{}{
		"total_consumed": totalConsumed,
		"by_category":    byCategory,
		"days":           days,
	})
}

// ─── 兑换码管理 ───────────────────────────────────────────

// handleAdminBillingCreateRedeemCodes 批量生成兑换码
// POST /api/v2/admin/billing/redeem-codes
func (s *Server) handleAdminBillingCreateRedeemCodes(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	var body struct {
		Count       int     `json:"count"`
		PointAmount float64 `json:"point_amount"`
		BatchLabel  string  `json:"batch_label"`
		ExpiresIn   int     `json:"expires_in_days"` // 0 = 永不过期
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.Count <= 0 || body.Count > 500 {
		response.Err(w, http.StatusBadRequest, "bad_request", "count must be 1-500")
		return
	}
	if body.PointAmount <= 0 {
		response.Err(w, http.StatusBadRequest, "bad_request", "point_amount must be positive")
		return
	}

	admin := userFromContext(r.Context())
	adminID := "admin"
	if admin != nil {
		adminID = admin.Sub
	}

	// 计算过期时间
	var expiresAt *time.Time
	if body.ExpiresIn > 0 {
		t := time.Now().AddDate(0, 0, body.ExpiresIn)
		expiresAt = &t
	}

	codes, err := s.billingRepo.CreateRedeemCodes(r.Context(), body.Count, body.PointAmount, body.BatchLabel, expiresAt, adminID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "create_failed", "failed to generate codes")
		return
	}

	slog.Info("admin: redeem codes created", "count", len(codes), "points_each", body.PointAmount, "admin", adminID)
	response.OK(w, map[string]interface{}{
		"codes": codes,
		"count": len(codes),
	})
}

// handleAdminBillingListRedeemCodes 兑换码列表
// GET /api/v2/admin/billing/redeem-codes?status=unused&page=1&limit=20
func (s *Server) handleAdminBillingListRedeemCodes(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.OK(w, map[string]interface{}{"codes": []interface{}{}, "total": 0})
		return
	}

	status := r.URL.Query().Get("status")
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	limit := parseIntDefault(r.URL.Query().Get("limit"), 20)
	offset := (page - 1) * limit

	codes, total, err := s.billingRepo.ListRedeemCodes(r.Context(), status, limit, offset)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list codes")
		return
	}

	response.OK(w, map[string]interface{}{
		"codes": codes,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// handleAdminBillingDisableRedeemCode 作废兑换码
// DELETE /api/v2/admin/billing/redeem-codes/:id
func (s *Server) handleAdminBillingDisableRedeemCode(w http.ResponseWriter, r *http.Request) {
	if s.billingRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
		return
	}

	codeID := r.PathValue("id")
	if codeID == "" {
		codeID = r.URL.Query().Get("id")
	}
	if codeID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "id is required")
		return
	}

	if err := s.billingRepo.DisableRedeemCode(r.Context(), codeID); err != nil {
		response.Err(w, http.StatusInternalServerError, "disable_failed", "failed to disable code")
		return
	}

	slog.Info("admin: redeem code disabled", "code_id", codeID)
	response.OK(w, map[string]interface{}{"ok": true})
}

var _ = fmt.Sprintf
