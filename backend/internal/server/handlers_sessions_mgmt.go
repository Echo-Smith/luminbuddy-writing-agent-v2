package server

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Multi-Device Session Management ──────────────────────
//
// This file implements the "多设备查看" (multi-device session viewing) feature.
//
// Every time issueToken is called, a unique jti (session ID) is generated and
// stored in the user_sessions table along with device metadata (user-agent,
// IP, device type). This enables:
//
//   - GET /api/v2/auth/sessions — list all active sessions for the current user
//   - DELETE /api/v2/auth/sessions/{id} — revoke a specific session (future)
//   - POST /auth/logout-all — revoke all sessions (future)
//
// The JWT middleware checks the user_sessions table to ensure the jti has not
// been revoked, enabling real-time device kick/offline.

// generateSessionID creates a random 16-byte hex string for use as JWT jti.
func generateSessionID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return time.Now().Format("20060102150405000000")
	}
	return hex.EncodeToString(buf)
}

// parseDeviceInfo extracts device name, type, and IP from the HTTP request.
func parseDeviceInfo(r *http.Request) (deviceName, deviceType, ip string) {
	ua := r.UserAgent()
	ip = getClientIP(r)

	// Detect device type from User-Agent
	uaLower := strings.ToLower(ua)
	switch {
	case strings.Contains(uaLower, "mobile") || strings.Contains(uaLower, "android"):
		deviceType = "mobile"
	case strings.Contains(uaLower, "ipad") || strings.Contains(uaLower, "tablet"):
		deviceType = "tablet"
	case strings.Contains(uaLower, "iphone"):
		deviceType = "mobile"
	default:
		deviceType = "desktop"
	}

	// Build a human-readable device name from User-Agent
	deviceName = parseBrowserOS(ua)
	if deviceName == "" {
		deviceName = "Unknown Device"
	}

	return deviceName, deviceType, ip
}

// parseBrowserOS extracts a short "Browser / OS" string from User-Agent.
func parseBrowserOS(ua string) string {
	var browser, os string

	// Detect OS
	switch {
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Mac OS X"):
		os = "macOS"
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
		os = "iOS"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}

	// Detect browser
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Safari/") && !strings.Contains(ua, "Chrome/"):
		browser = "Safari"
	}

	if browser != "" && os != "" {
		return browser + " / " + os
	} else if browser != "" {
		return browser
	} else if os != "" {
		return os
	}
	return ""
}

// recordSession stores a new session in the user_sessions table.
// Called from issueToken after JWT generation.
func (s *Server) recordSession(r *http.Request, userID, jti string) {
	if s.db == nil {
		return
	}

	deviceName, deviceType, ip := parseDeviceInfo(r)
	expiresAt := time.Now().Add(s.cfg.JWT.Expiry)

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO user_sessions (user_id, jti, device_name, device_type, ip_address, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (jti) DO NOTHING
	`, userID, jti, deviceName, deviceType, ip, r.UserAgent(), expiresAt)

	if err != nil {
		slog.Warn("failed to record user session", "error", err, "user_id", userID)
	}
}

// updateSessionActivity updates last_active_at for a session.
// Called from jwtAuthMiddleware on each authenticated request.
func (s *Server) updateSessionActivity(jti string) {
	if s.db == nil || jti == "" {
		return
	}

	_, err := s.db.Exec(`
		UPDATE user_sessions SET last_active_at = NOW()
		WHERE jti = $1 AND revoked = FALSE
	`, jti)
	if err != nil {
		slog.Debug("failed to update session activity", "error", err, "jti", jti)
	}
}

// isSessionRevoked checks if a session has been revoked.
// Called from ValidateJWT (via jwtAuthMiddleware) to enforce revocation.
func (s *Server) isSessionRevoked(jti string) bool {
	if s.db == nil || jti == "" {
		return false
	}

	var revoked bool
	err := s.db.QueryRow(`
		SELECT revoked FROM user_sessions WHERE jti = $1
	`, jti).Scan(&revoked)
	if err != nil {
		// If session not found, don't block (backward compat for old tokens without jti)
		return false
	}
	return revoked
}

// ─── API Handlers ─────────────────────────────────────────

// handleListUserActiveSessions lists all active sessions for the authenticated user.
//
// GET /api/v2/auth/sessions
// Header: Authorization: Bearer <jwt>
func (s *Server) handleListUserActiveSessions(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if s.db == nil {
		response.OK(w, map[string]interface{}{
			"sessions": []interface{}{},
			"total":    0,
		})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id::text, jti, device_name, device_type, ip_address,
		       created_at, last_active_at, expires_at,
		       (jti = $2) AS is_current
		FROM user_sessions
		WHERE user_id = $1 AND revoked = FALSE AND expires_at > NOW()
		ORDER BY last_active_at DESC
	`, user.Sub, user.Jti)
	if err != nil {
		slog.Warn("failed to list user sessions", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list sessions")
		return
	}
	defer rows.Close()

	sessions := []map[string]interface{}{}
	for rows.Next() {
		var (
			id           string
			jti          string
			deviceName   string
			deviceType   string
			ipAddress    string
			createdAt    time.Time
			lastActiveAt time.Time
			expiresAt    time.Time
			isCurrent    bool
		)
		if err := rows.Scan(&id, &jti, &deviceName, &deviceType, &ipAddress,
			&createdAt, &lastActiveAt, &expiresAt, &isCurrent); err != nil {
			slog.Warn("failed to scan session row", "error", err)
			continue
		}

		sessions = append(sessions, map[string]interface{}{
			"id":             id,
			"jti":            jti,
			"device_name":    deviceName,
			"device_type":    deviceType,
			"ip_address":     maskIP(ipAddress),
			"created_at":     createdAt,
			"last_active_at": lastActiveAt,
			"expires_at":     expiresAt,
			"is_current":     isCurrent,
		})
	}

	response.OK(w, map[string]interface{}{
		"sessions":     sessions,
		"total":        len(sessions),
		"online_count": s.sseHub.UserClientCount(user.Sub),
	})
}

// maskIP masks the last octet of an IPv4 address or the last 2 hextets of IPv6
// for privacy. e.g. "192.168.1.100" → "192.168.1.*"
func maskIP(ip string) string {
	if ip == "" || ip == "anonymous" {
		return ""
	}

	if strings.Contains(ip, ".") {
		// IPv4
		parts := strings.Split(ip, ".")
		if len(parts) == 4 {
			return strings.Join(parts[:3], ".") + ".*"
		}
		return ip
	}

	if strings.Contains(ip, ":") {
		// IPv6 — mask last 2 groups
		parts := strings.Split(ip, ":")
		if len(parts) >= 4 {
			return strings.Join(parts[:len(parts)-2], ":") + ":*:*"
		}
		return ip
	}

	return ip
}
