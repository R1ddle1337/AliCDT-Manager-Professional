package controller

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// AdminSession is the safe, non-secret projection of an administrator
// session. The token itself is never returned; ID is only a short hash prefix
// that can be used to revoke a session.
type AdminSession struct {
	ID         string     `json:"id"`
	Current    bool       `json:"current"`
	IPAddress  string     `json:"ip_address,omitempty"`
	UserAgent  string     `json:"user_agent,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

const (
	loginFailureWindow = 15 * time.Minute
	loginFailureLimit  = 8
)

var ErrAdminTwoFactorRequired = errors.New("two-factor authentication code is required")

type loginFailureState struct {
	Count       int
	WindowStart time.Time
	BlockedTill time.Time
}

// clientAddress deliberately uses RemoteAddr instead of trusting arbitrary
// X-Forwarded-For headers. The reverse proxy can pass a trusted address via
// X-Real-IP when the deployment needs per-client throttling.
func clientAddress(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Real-IP")); value != "" {
		if net.ParseIP(value) != nil {
			return value
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (s *Server) loginAllowed(address string) (bool, time.Duration) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	for staleAddress, staleState := range s.loginFailures {
		if now.Sub(staleState.WindowStart) >= loginFailureWindow && (staleState.BlockedTill.IsZero() || now.After(staleState.BlockedTill)) {
			delete(s.loginFailures, staleAddress)
		}
	}
	state, ok := s.loginFailures[address]
	if !ok {
		return true, 0
	}
	if !state.BlockedTill.IsZero() && now.Before(state.BlockedTill) {
		return false, time.Until(state.BlockedTill)
	}
	if now.Sub(state.WindowStart) >= loginFailureWindow {
		delete(s.loginFailures, address)
		return true, 0
	}
	return true, 0
}

func (s *Server) recordLoginFailure(address string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	state := s.loginFailures[address]
	if state.WindowStart.IsZero() || now.Sub(state.WindowStart) >= loginFailureWindow {
		state = loginFailureState{WindowStart: now}
	}
	state.Count++
	if state.Count >= loginFailureLimit {
		state.BlockedTill = now.Add(loginFailureWindow)
	}
	s.loginFailures[address] = state
}

func (s *Server) recordLoginSuccess(address string) {
	s.loginMu.Lock()
	delete(s.loginFailures, address)
	s.loginMu.Unlock()
}

func parseSessionTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func (s *Store) ListAdminSessions(ctx context.Context, currentToken string) ([]AdminSession, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE expires_at<=?`, now); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT token_hash,COALESCE(ip_address,''),COALESCE(user_agent,''),created_at,expires_at,last_seen_at FROM admin_sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	currentHash := hashSecret(currentToken)
	sessions := make([]AdminSession, 0)
	for rows.Next() {
		var tokenHash, ipAddress, userAgent, createdAt, expiresAt string
		var lastSeenAt sql.NullString
		if err := rows.Scan(&tokenHash, &ipAddress, &userAgent, &createdAt, &expiresAt, &lastSeenAt); err != nil {
			return nil, err
		}
		created, err := parseSessionTime(createdAt)
		if err != nil {
			continue
		}
		expires, err := parseSessionTime(expiresAt)
		if err != nil {
			continue
		}
		sessionID := tokenHash
		if len(sessionID) > 12 {
			sessionID = sessionID[:12]
		}
		var lastSeen *time.Time
		if lastSeenAt.Valid {
			if parsed, parseErr := parseSessionTime(lastSeenAt.String); parseErr == nil {
				lastSeen = &parsed
			}
		}
		sessions = append(sessions, AdminSession{ID: sessionID, Current: subtle.ConstantTimeCompare([]byte(tokenHash), []byte(currentHash)) == 1, IPAddress: ipAddress, UserAgent: userAgent, CreatedAt: created, ExpiresAt: expires, LastSeenAt: lastSeen})
	}
	return sessions, rows.Err()
}

func (s *Store) RecordAdminSessionMetadata(ctx context.Context, token, ipAddress, userAgent string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE admin_sessions SET ip_address=?,user_agent=?,last_seen_at=? WHERE token_hash=?`, strings.TrimSpace(ipAddress), strings.TrimSpace(userAgent), time.Now().UTC().Format(time.RFC3339Nano), hashSecret(token))
	return err
}

func (s *Store) RevokeAdminSession(ctx context.Context, sessionID string) error {
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	if len(sessionID) < 8 || len(sessionID) > 64 {
		return errors.New("invalid session id")
	}
	for _, char := range sessionID {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return errors.New("invalid session id")
		}
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE substr(token_hash,1,?)=?`, len(sessionID), sessionID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RevokeOtherAdminSessions(ctx context.Context, currentToken string) error {
	currentHash := hashSecret(strings.TrimSpace(currentToken))
	if strings.TrimSpace(currentToken) == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions`)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash<>?`, currentHash)
	return err
}

func strongAdminPassword(password string) error {
	if password != strings.TrimSpace(password) {
		return errors.New("password must not start or end with whitespace")
	}
	if len(password) < 10 || len(password) > 256 {
		return errors.New("password must contain 10 to 256 characters")
	}
	var upper, lower, digit bool
	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			upper = true
		case unicode.IsLower(char):
			lower = true
		case unicode.IsDigit(char):
			digit = true
		}
	}
	if !upper || !lower || !digit {
		return errors.New("password must contain uppercase, lowercase letters, and numbers")
	}
	return nil
}

func (s *Store) ChangeAdminPasswordWithCurrent(ctx context.Context, currentPassword, newPassword string) error {
	if err := strongAdminPassword(newPassword); err != nil {
		return err
	}
	var username, passwordHash string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='admin_username'`).Scan(&username); err != nil {
		return errors.New("administrator is not initialized")
	}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='admin_password_hash'`).Scan(&passwordHash); err != nil {
		return errors.New("administrator is not initialized")
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(currentPassword)) != nil {
		return errors.New("current password is incorrect")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET value=? WHERE key='admin_password_hash'`, string(hash)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions`); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO logs(level,category,message,created_at) VALUES(?,?,?,?)`, "info", "security", fmt.Sprintf("管理员 %s 修改了登录密码，全部会话已撤销", username), now); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeTOTPSecret(value string) string {
	return strings.TrimRight(strings.ToUpper(strings.TrimSpace(value)), "=")
}

func generateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

func totpCode(secret string, timestamp time.Time) (string, error) {
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalizeTOTPSecret(secret))
	if err != nil || len(decoded) == 0 {
		return "", errors.New("invalid two-factor secret")
	}
	counter := uint64(timestamp.Unix() / 30)
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	hash := hmac.New(sha1.New, decoded)
	_, _ = hash.Write(message[:])
	sum := hash.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	number := (uint32(sum[offset])&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
	return fmt.Sprintf("%06d", number%1000000), nil
}

func verifyTOTPCode(secret, code string, timestamp time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	if _, err := strconv.Atoi(code); err != nil {
		return false
	}
	for offset := -1; offset <= 1; offset++ {
		expected, err := totpCode(secret, timestamp.Add(time.Duration(offset)*30*time.Second))
		if err == nil && subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func (s *Store) AdminTwoFAEnabled(ctx context.Context) (bool, error) {
	secret, err := s.GetSetting(ctx, "admin_totp_secret")
	return normalizeTOTPSecret(secret) != "", err
}

func (s *Store) BeginAdminTwoFA(ctx context.Context, username string) (string, string, error) {
	enabled, err := s.AdminTwoFAEnabled(ctx)
	if err != nil {
		return "", "", err
	}
	if enabled {
		return "", "", errors.New("two-factor authentication is already enabled")
	}
	secret, err := generateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('admin_totp_pending',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, secret); err != nil {
		return "", "", err
	}
	issuer := "AliCDT"
	account := issuer + ":" + strings.TrimSpace(username)
	uri := "otpauth://totp/" + url.PathEscape(account) + "?secret=" + secret + "&issuer=" + url.QueryEscape(issuer)
	return secret, uri, nil
}

func (s *Store) ConfirmAdminTwoFA(ctx context.Context, code string) error {
	pending, err := s.GetSetting(ctx, "admin_totp_pending")
	if err != nil || normalizeTOTPSecret(pending) == "" {
		return errors.New("two-factor setup has not been started")
	}
	pending = normalizeTOTPSecret(pending)
	if !verifyTOTPCode(pending, code, time.Now().UTC()) {
		return errors.New("invalid two-factor code")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('admin_totp_secret',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, pending); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key='admin_totp_pending'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO logs(level,category,message,created_at) VALUES(?,?,?,?)`, "info", "security", "管理员启用了双因素认证，全部会话已撤销", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DisableAdminTwoFA(ctx context.Context, code string) error {
	secret, err := s.GetSetting(ctx, "admin_totp_secret")
	if err != nil || normalizeTOTPSecret(secret) == "" {
		return errors.New("two-factor authentication is not enabled")
	}
	if !verifyTOTPCode(secret, code, time.Now().UTC()) {
		return errors.New("invalid two-factor code")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key IN ('admin_totp_secret','admin_totp_pending')`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO logs(level,category,message,created_at) VALUES(?,?,?,?)`, "warning", "security", "管理员关闭了双因素认证，全部会话已撤销", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LoginAdminWithCode(ctx context.Context, username, password, code string) (string, error) {
	var storedUsername, passwordHash string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='admin_username'`).Scan(&storedUsername); err != nil {
		return "", errors.New("administrator is not initialized")
	}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='admin_password_hash'`).Scan(&passwordHash); err != nil {
		return "", errors.New("administrator is not initialized")
	}
	if !subtleStringCompare(username, storedUsername) || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return "", errors.New("invalid username or password")
	}
	secret, err := s.GetSetting(ctx, "admin_totp_secret")
	if err != nil {
		return "", err
	}
	if normalizeTOTPSecret(secret) != "" && !verifyTOTPCode(secret, code, time.Now().UTC()) {
		return "", ErrAdminTwoFactorRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	token, err := createAdminSession(ctx, tx, storedUsername)
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) listAdminSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.ListAdminSessions(r.Context(), bearerToken(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) revokeAdminSession(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RevokeAdminSession(r.Context(), chi.URLParam(r, "sessionID")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeOtherAdminSessions(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RevokeOtherAdminSessions(r.Context(), bearerToken(r)); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) changeAdminPassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.ChangeAdminPasswordWithCurrent(r.Context(), request.CurrentPassword, request.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "密码已更新，全部管理员会话已撤销，请重新登录"})
}

func (s *Server) adminTwoFAStatus(w http.ResponseWriter, r *http.Request) {
	enabled, err := s.store.AdminTwoFAEnabled(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (s *Server) beginAdminTwoFA(w http.ResponseWriter, r *http.Request) {
	principal, ok := consolePrincipalFromContext(r.Context())
	if !ok || principal.Role != consoleRoleAdmin {
		writeError(w, http.StatusForbidden, errors.New("administrator access is required"))
		return
	}
	secret, uri, err := s.store.BeginAdminTwoFA(r.Context(), principal.Username)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_uri": uri})
}

func (s *Server) confirmAdminTwoFA(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.ConfirmAdminTwoFA(r.Context(), request.Code); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "双因素认证已启用，全部会话已撤销，请重新登录"})
}

func (s *Server) disableAdminTwoFA(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.DisableAdminTwoFA(r.Context(), request.Code); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "双因素认证已关闭，全部会话已撤销，请重新登录"})
}

// Keep the login limiter independent of request handling locks. A map is small
// and bounded by naturally expiring entries; the periodic cleanup avoids stale
// addresses growing forever on public installations.
func (s *Server) cleanupLoginFailures() {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	for address, state := range s.loginFailures {
		if now.Sub(state.WindowStart) >= loginFailureWindow && (state.BlockedTill.IsZero() || now.After(state.BlockedTill)) {
			delete(s.loginFailures, address)
		}
	}
}
