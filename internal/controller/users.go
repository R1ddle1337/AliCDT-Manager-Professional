package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
	"golang.org/x/crypto/bcrypt"
)

// ConsoleUser is a restricted control-panel account. Its usage comes from the
// assigned standalone Agent service when present, otherwise from the
// account-level CDT snapshots explicitly assigned by an administrator.
type ConsoleUser struct {
	ID                   int64                     `json:"id"`
	Username             string                    `json:"username"`
	DisplayName          string                    `json:"display_name"`
	Enabled              bool                      `json:"enabled"`
	TrafficLimitGB       float64                   `json:"traffic_limit_gb"`
	TrafficUsedGB        float64                   `json:"traffic_used_gb"`
	TrafficRemainingGB   float64                   `json:"traffic_remaining_gb"`
	TrafficPercent       float64                   `json:"traffic_percent"`
	TrafficKnown         bool                      `json:"traffic_known"`
	TrafficSource        string                    `json:"traffic_source"`
	BytesUp              uint64                    `json:"bytes_up"`
	BytesDown            uint64                    `json:"bytes_down"`
	BilledBytes          uint64                    `json:"billed_bytes"`
	CDTTrafficUsedGB     float64                   `json:"cdt_traffic_used_gb"`
	CDTTrafficKnown      bool                      `json:"cdt_traffic_known"`
	AssignedAccountCount int                       `json:"assigned_account_count"`
	Accounts             []ConsoleUserCloudAccount `json:"accounts"`
	RelayService         *ConsoleUserRelayService  `json:"relay_service,omitempty"`
	LastLoginAt          *time.Time                `json:"last_login_at,omitempty"`
	CreatedAt            *time.Time                `json:"created_at,omitempty"`
	UpdatedAt            *time.Time                `json:"updated_at,omitempty"`
}

type ConsoleUserRelayService struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RelayNodeName string `json:"relay_node_name"`
	BillingMode   string `json:"billing_mode"`
	BillingEpoch  int64  `json:"billing_epoch"`
	Reported      bool   `json:"reported"`
	Listening     bool   `json:"listening"`
	QuotaExceeded bool   `json:"quota_exceeded"`
}

type ConsoleUserCloudAccount struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	TrafficUsed  float64    `json:"traffic_used_gb"`
	TrafficKnown bool       `json:"traffic_known"`
	SyncedAt     *time.Time `json:"synced_at,omitempty"`
}

type ConsoleUserRequest struct {
	Username       string  `json:"username"`
	DisplayName    string  `json:"display_name"`
	Password       string  `json:"password"`
	Enabled        *bool   `json:"enabled,omitempty"`
	TrafficLimitGB float64 `json:"traffic_limit_gb"`
	AccountIDs     []int64 `json:"account_ids"`
}

func (s *Store) ListConsoleUsers(ctx context.Context) ([]ConsoleUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,username,display_name,enabled,traffic_limit_gb,last_login_at,created_at,updated_at FROM console_users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	users := make([]ConsoleUser, 0)
	for rows.Next() {
		var user ConsoleUser
		var enabled int
		var lastLoginAt, createdAt, updatedAt sql.NullString
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &enabled, &user.TrafficLimitGB, &lastLoginAt, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		user.Enabled = enabled != 0
		user.Accounts = make([]ConsoleUserCloudAccount, 0)
		user.LastLoginAt = nullableDatabaseTime(lastLoginAt)
		user.CreatedAt = nullableDatabaseTime(createdAt)
		user.UpdatedAt = nullableDatabaseTime(updatedAt)
		users = append(users, user)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	byID := make(map[int64]int, len(users))
	for index := range users {
		byID[users[index].ID] = index
	}
	accountRows, err := s.db.QueryContext(ctx, `SELECT uca.user_id,a.id,a.name,COALESCE(ts.used_gb,0),ts.account_id,ts.synced_at
		FROM user_cloud_accounts uca
		JOIN accounts a ON a.id=uca.account_id
		LEFT JOIN account_traffic_snapshots ts ON ts.account_id=a.id
		ORDER BY uca.user_id,a.id`)
	if err != nil {
		return nil, err
	}
	knownCounts := make(map[int64]int)
	for accountRows.Next() {
		var userID int64
		var account ConsoleUserCloudAccount
		var snapshotID sql.NullInt64
		var syncedAt sql.NullString
		if err := accountRows.Scan(&userID, &account.ID, &account.Name, &account.TrafficUsed, &snapshotID, &syncedAt); err != nil {
			accountRows.Close()
			return nil, err
		}
		index, exists := byID[userID]
		if !exists {
			continue
		}
		account.TrafficKnown = snapshotID.Valid
		account.SyncedAt = nullableDatabaseTime(syncedAt)
		users[index].Accounts = append(users[index].Accounts, account)
		users[index].TrafficUsedGB += account.TrafficUsed
		if account.TrafficKnown {
			knownCounts[userID]++
		}
	}
	if err := accountRows.Close(); err != nil {
		return nil, err
	}
	for index := range users {
		user := &users[index]
		user.AssignedAccountCount = len(user.Accounts)
		user.TrafficKnown = user.AssignedAccountCount > 0 && knownCounts[user.ID] == user.AssignedAccountCount
		user.CDTTrafficUsedGB = user.TrafficUsedGB
		user.CDTTrafficKnown = user.TrafficKnown
		user.TrafficSource = "cloud_account"
		user.TrafficRemainingGB = user.TrafficLimitGB - user.TrafficUsedGB
		if user.TrafficRemainingGB < 0 {
			user.TrafficRemainingGB = 0
		}
		if user.TrafficLimitGB > 0 {
			user.TrafficPercent = user.TrafficUsedGB / user.TrafficLimitGB * 100
		}
	}
	serviceRows, err := s.db.QueryContext(ctx, `SELECT rs.user_id,rs.id,rs.name,rn.name,COALESCE(rs.billing_mode,'both'),COALESCE(rs.billing_epoch,1),COALESCE(rn.service_status_json,'[]')
		FROM relay_services rs JOIN relay_nodes rn ON rn.id=rs.relay_node_id WHERE rs.user_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	for serviceRows.Next() {
		var userID int64
		var service ConsoleUserRelayService
		var rawStatus string
		if err := serviceRows.Scan(&userID, &service.ID, &service.Name, &service.RelayNodeName, &service.BillingMode, &service.BillingEpoch, &rawStatus); err != nil {
			serviceRows.Close()
			return nil, err
		}
		service.BillingEpoch = effectiveBillingEpoch(service.BillingEpoch)
		index, exists := byID[userID]
		if !exists {
			continue
		}
		users[index].TrafficSource = "agent"
		users[index].TrafficKnown = false
		users[index].TrafficUsedGB = 0
		users[index].BytesUp = 0
		users[index].BytesDown = 0
		users[index].BilledBytes = 0
		var statuses []protocol.ServiceStatus
		_ = json.Unmarshal([]byte(rawStatus), &statuses)
		for _, status := range statuses {
			if status.ID != service.ID {
				continue
			}
			service.Listening = status.Listening
			service.QuotaExceeded = status.QuotaExceeded
			if status.BillingMode != "" && status.BillingEpoch == service.BillingEpoch {
				service.Reported = true
				users[index].BytesUp = status.BytesUp
				users[index].BytesDown = status.BytesDown
				users[index].BilledBytes = billedBytesFromStatus(status, service.BillingMode)
				users[index].TrafficUsedGB = float64(users[index].BilledBytes) / (1024 * 1024 * 1024)
				users[index].TrafficKnown = true
				users[index].TrafficSource = "agent"
			}
			break
		}
		users[index].RelayService = &service
	}
	if err := serviceRows.Close(); err != nil {
		return nil, err
	}
	for index := range users {
		user := &users[index]
		if user.RelayService != nil {
			user.TrafficRemainingGB = user.TrafficLimitGB - user.TrafficUsedGB
			if user.TrafficRemainingGB < 0 {
				user.TrafficRemainingGB = 0
			}
			if user.TrafficLimitGB > 0 {
				user.TrafficPercent = user.TrafficUsedGB / user.TrafficLimitGB * 100
			}
		}
	}
	return users, nil
}

func billedBytesFromStatus(status protocol.ServiceStatus, mode string) uint64 {
	if status.BilledBytes > 0 {
		return status.BilledBytes
	}
	switch mode {
	case protocol.BillingModeUpload, protocol.BillingModeIngress:
		return status.BytesUp
	case protocol.BillingModeDownload, protocol.BillingModeEgress:
		return status.BytesDown
	default:
		return status.BytesUp + status.BytesDown
	}
}

func (s *Store) ConsoleUserOverview(ctx context.Context, userID int64) (ConsoleUser, error) {
	users, err := s.ListConsoleUsers(ctx)
	if err != nil {
		return ConsoleUser{}, err
	}
	for _, user := range users {
		if user.ID == userID {
			return user, nil
		}
	}
	return ConsoleUser{}, sql.ErrNoRows
}

func (s *Store) CreateConsoleUser(ctx context.Context, request ConsoleUserRequest) (ConsoleUser, error) {
	request, enabled, err := normalizeConsoleUserRequest(request, false, true)
	if err != nil {
		return ConsoleUser{}, err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		return ConsoleUser{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConsoleUser{}, err
	}
	defer tx.Rollback()
	if err := consoleUsernameAvailable(ctx, tx, request.Username, 0); err != nil {
		return ConsoleUser{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `INSERT INTO console_users(username,display_name,password_hash,enabled,traffic_limit_gb,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		request.Username, request.DisplayName, string(passwordHash), boolInt(enabled), request.TrafficLimitGB, now, now)
	if err != nil {
		return ConsoleUser{}, err
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return ConsoleUser{}, err
	}
	if err := replaceConsoleUserAccounts(ctx, tx, userID, request.AccountIDs); err != nil {
		return ConsoleUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConsoleUser{}, err
	}
	return s.ConsoleUserOverview(ctx, userID)
}

func (s *Store) UpdateConsoleUser(ctx context.Context, userID int64, request ConsoleUserRequest) (ConsoleUser, error) {
	request, _, err := normalizeConsoleUserRequest(request, true, request.Enabled != nil)
	if err != nil {
		return ConsoleUser{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConsoleUser{}, err
	}
	defer tx.Rollback()
	var currentEnabled int
	var currentTrafficLimit float64
	if err := tx.QueryRowContext(ctx, `SELECT enabled,traffic_limit_gb FROM console_users WHERE id=?`, userID).Scan(&currentEnabled, &currentTrafficLimit); err != nil {
		return ConsoleUser{}, err
	}
	if err := consoleUsernameAvailable(ctx, tx, request.Username, userID); err != nil {
		return ConsoleUser{}, err
	}
	enabled := currentEnabled != 0
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	passwordChanged := request.Password != ""
	if passwordChanged {
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
		if err != nil {
			return ConsoleUser{}, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE console_users SET username=?,display_name=?,password_hash=?,enabled=?,traffic_limit_gb=?,updated_at=? WHERE id=?`,
			request.Username, request.DisplayName, string(passwordHash), boolInt(enabled), request.TrafficLimitGB, now, userID)
		if err != nil {
			return ConsoleUser{}, err
		}
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE console_users SET username=?,display_name=?,enabled=?,traffic_limit_gb=?,updated_at=? WHERE id=?`,
			request.Username, request.DisplayName, boolInt(enabled), request.TrafficLimitGB, now, userID)
		if err != nil {
			return ConsoleUser{}, err
		}
	}
	if request.AccountIDs != nil {
		if err := replaceConsoleUserAccounts(ctx, tx, userID, request.AccountIDs); err != nil {
			return ConsoleUser{}, err
		}
	}
	if currentTrafficLimit != request.TrafficLimitGB {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_services SET traffic_limit_gb=?,updated_at=? WHERE user_id=?`, request.TrafficLimitGB, now, userID); err != nil {
			return ConsoleUser{}, err
		}
	}
	if currentEnabled != boolInt(enabled) || currentTrafficLimit != request.TrafficLimitGB {
		if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id IN (SELECT relay_node_id FROM relay_services WHERE user_id=?)`, userID); err != nil {
			return ConsoleUser{}, err
		}
	}
	if passwordChanged || !enabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE user_id=?`, userID); err != nil {
			return ConsoleUser{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ConsoleUser{}, err
	}
	return s.ConsoleUserOverview(ctx, userID)
}

func (s *Store) DeleteConsoleUser(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE relay_nodes SET desired_revision=desired_revision+1 WHERE id IN (SELECT relay_node_id FROM relay_services WHERE user_id=?)`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relay_services SET enabled=0,user_id=NULL,updated_at=? WHERE user_id=?`, time.Now().UTC().Format(time.RFC3339Nano), userID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM console_users WHERE id=?`, userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) LoginConsoleUser(ctx context.Context, username, password string) (string, ConsoleUser, error) {
	username = strings.TrimSpace(username)
	var userID int64
	var passwordHash string
	var enabled int
	if err := s.db.QueryRowContext(ctx, `SELECT id,password_hash,enabled FROM console_users WHERE username=? COLLATE NOCASE`, username).Scan(&userID, &passwordHash, &enabled); err != nil {
		return "", ConsoleUser{}, errors.New("invalid username or password")
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return "", ConsoleUser{}, errors.New("invalid username or password")
	}
	if enabled == 0 {
		return "", ConsoleUser{}, errors.New("user account is disabled")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", ConsoleUser{}, err
	}
	defer tx.Rollback()
	raw := randomSecret(32)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_sessions(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)`,
		hashSecret(raw), userID, now.Add(7*24*time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return "", ConsoleUser{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE console_users SET last_login_at=?,updated_at=? WHERE id=?`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), userID); err != nil {
		return "", ConsoleUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", ConsoleUser{}, err
	}
	user, err := s.ConsoleUserOverview(ctx, userID)
	return raw, user, err
}

func (s *Store) AuthenticateUserSession(ctx context.Context, token string) (ConsoleUser, error) {
	if token == "" {
		return ConsoleUser{}, errors.New("missing session")
	}
	var userID int64
	var expires string
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT us.user_id,us.expires_at,u.enabled FROM user_sessions us JOIN console_users u ON u.id=us.user_id WHERE us.token_hash=?`, hashSecret(token)).Scan(&userID, &expires, &enabled)
	if err != nil {
		return ConsoleUser{}, errors.New("invalid session")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || time.Now().UTC().After(expiresAt) || enabled == 0 {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE token_hash=?`, hashSecret(token))
		return ConsoleUser{}, errors.New("session expired")
	}
	return s.ConsoleUserOverview(ctx, userID)
}

func (s *Store) DeleteConsoleSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	hash := hashSecret(token)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash=?`, hash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE token_hash=?`, hash); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AdminSessionUsername(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", errors.New("missing session")
	}
	var username, expires string
	if err := s.db.QueryRowContext(ctx, `SELECT username,expires_at FROM admin_sessions WHERE token_hash=?`, hashSecret(token)).Scan(&username, &expires); err != nil {
		return "", errors.New("invalid session")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || time.Now().UTC().After(expiresAt) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash=?`, hashSecret(token))
		return "", errors.New("session expired")
	}
	return username, nil
}

func normalizeConsoleUserRequest(request ConsoleUserRequest, passwordOptional, enabledProvided bool) (ConsoleUserRequest, bool, error) {
	request.Username = strings.TrimSpace(request.Username)
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	if len(request.Username) < 3 || len(request.Username) > 64 || strings.ContainsAny(request.Username, " \t\r\n") {
		return request, false, errors.New("username must contain 3 to 64 non-space characters")
	}
	if request.DisplayName == "" {
		request.DisplayName = request.Username
	}
	if len(request.DisplayName) > 100 {
		return request, false, errors.New("display name must not exceed 100 characters")
	}
	if (!passwordOptional || request.Password != "") && len(request.Password) < 8 {
		return request, false, errors.New("password must contain at least 8 characters")
	}
	if request.TrafficLimitGB <= 0 || request.TrafficLimitGB > 1_000_000_000 {
		return request, false, errors.New("traffic limit must be greater than 0 GB")
	}
	enabled := true
	if enabledProvided && request.Enabled != nil {
		enabled = *request.Enabled
	}
	return request, enabled, nil
}

func consoleUsernameAvailable(ctx context.Context, tx *sql.Tx, username string, excludedUserID int64) error {
	var adminUsername string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(value,'') FROM settings WHERE key='admin_username'`).Scan(&adminUsername)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if strings.EqualFold(username, adminUsername) {
		return errors.New("username is reserved by the administrator")
	}
	var existingID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM console_users WHERE username=? COLLATE NOCASE AND id<>?`, username, excludedUserID).Scan(&existingID)
	if err == nil {
		return errors.New("username already exists")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func replaceConsoleUserAccounts(ctx context.Context, tx *sql.Tx, userID int64, accountIDs []int64) error {
	unique := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			return errors.New("invalid cloud account id")
		}
		if _, exists := unique[accountID]; exists {
			continue
		}
		unique[accountID] = struct{}{}
		var ownerID sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT (SELECT user_id FROM user_cloud_accounts WHERE account_id=a.id) FROM accounts a WHERE a.id=?`, accountID).Scan(&ownerID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("cloud account %d does not exist", accountID)
		}
		if err != nil {
			return err
		}
		if ownerID.Valid && ownerID.Int64 != userID {
			return fmt.Errorf("cloud account %d is already assigned to another user", accountID)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_cloud_accounts WHERE user_id=?`, userID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for accountID := range unique {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_cloud_accounts(user_id,account_id,created_at) VALUES(?,?,?)`, userID, accountID, now); err != nil {
			return err
		}
	}
	return nil
}

func assignCloudAccountUser(ctx context.Context, tx *sql.Tx, accountID int64, userID *int64) error {
	if userID == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_cloud_accounts WHERE account_id=?`, accountID); err != nil {
		return err
	}
	if *userID <= 0 {
		return nil
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM console_users WHERE id=?`, *userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("assigned user does not exist")
		}
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO user_cloud_accounts(user_id,account_id,created_at) VALUES(?,?,?)`, *userID, accountID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func nullableDatabaseTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed := parseDatabaseTime(value.String)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}

func normalizeBillingMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", protocol.BillingModeBoth:
		return protocol.BillingModeBoth, nil
	case protocol.BillingModeUpload, protocol.BillingModeIngress:
		return protocol.BillingModeUpload, nil
	case protocol.BillingModeDownload, protocol.BillingModeEgress:
		return protocol.BillingModeDownload, nil
	default:
		return "", errors.New("billing mode must be upload, download or both")
	}
}

func billingEpochNow() int64 {
	return time.Now().UTC().Unix()
}

func effectiveBillingEpoch(value int64) int64 {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Now().In(location)
	if value > 1 {
		at := time.Unix(value, 0).In(location)
		if at.Year() == now.Year() && at.Month() == now.Month() {
			return value
		}
	}
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location).Unix()
}

func nextBillingEpoch(current int64) int64 {
	next := billingEpochNow()
	if next <= current {
		next = current + 1
	}
	return next
}

func nullablePositiveID(value *int64) interface{} {
	if value == nil || *value <= 0 {
		return nil
	}
	return *value
}

func nullableIDEqual(current sql.NullInt64, requested *int64) bool {
	if requested == nil || *requested <= 0 {
		return !current.Valid
	}
	return current.Valid && current.Int64 == *requested
}

func validateRelayServiceUserTx(ctx context.Context, tx *sql.Tx, userID *int64, excludeServiceID string) error {
	if userID == nil || *userID <= 0 {
		return nil
	}
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM console_users WHERE id=?`, *userID).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("assigned user does not exist")
		}
		return err
	}
	var serviceID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM relay_services WHERE user_id=? AND id<>?`, *userID, excludeServiceID).Scan(&serviceID)
	if err == nil {
		return errors.New("user is already assigned to another relay service")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func assignedRelayServiceLimitTx(ctx context.Context, tx *sql.Tx, userID *int64, configured float64) (float64, error) {
	if userID == nil || *userID <= 0 {
		return configured, nil
	}
	var limit float64
	if err := tx.QueryRowContext(ctx, `SELECT traffic_limit_gb FROM console_users WHERE id=?`, *userID).Scan(&limit); err != nil {
		return 0, err
	}
	return limit, nil
}
