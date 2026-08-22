package server

import (
	"log/slog"
	"net/http"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Billing Middleware ──────────────────────────────────
//
// 在写作 API 请求开始前检查用户点数余额。
// 如果余额不足，返回 402 Payment Required。
//
// 注意：此 middleware 只做"余额 > 0"的基本检查。
// 精确的预扣和结算在 harness 完成后由 SettleWritingPoints 处理，
// 因为写作实际消耗的 Token 数只有在 LLM 调用完成后才能确定。

// BillingMiddleware 检查用户余额
func (s *Server) BillingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.billingRepo == nil {
			next.ServeHTTP(w, r)
			return
		}

		user := userFromContext(r.Context())
		if user == nil || user.Sub == "" || user.Sub == "anonymous" {
			next.ServeHTTP(w, r)
			return
		}

		balance, ok, err := s.CheckUserBalance(r.Context(), user.Sub, 0)
		if err != nil {
			slog.Warn("billing middleware: balance check error", "error", err, "user_id", user.Sub)
			// 出错时不阻塞请求，允许通过（fail-open）
			next.ServeHTTP(w, r)
			return
		}

		if !ok {
			slog.Info("billing middleware: insufficient balance", "user_id", user.Sub, "balance", balance)
			response.Err(w, http.StatusPaymentRequired, "insufficient_balance",
				"点数余额不足，请充值后继续使用")
			return
		}

		next.ServeHTTP(w, r)
	})
}
