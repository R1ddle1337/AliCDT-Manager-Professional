package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/aliyun"
)

type instanceStatusClient interface {
	GetInstanceStatus(context.Context, string) (string, error)
}

type deleteInstanceClient interface {
	DeleteInstance(context.Context, string) error
}

type billingClient interface {
	GetBalance(context.Context) (aliyun.AccountBalance, error)
	GetBillOverview(context.Context, string) (aliyun.BillOverview, error)
}

type BillingResponse struct {
	Balance  *aliyun.AccountBalance `json:"balance"`
	Bill     *aliyun.BillOverview   `json:"bill"`
	Errors   []string               `json:"errors"`
	Disabled bool                   `json:"disabled,omitempty"`
}

func (s *CloudService) SyncAccountByID(ctx context.Context, accountID int64) (CloudSyncResult, error) {
	// Manual/legacy sync requests must share the same gate as the scheduler.
	// Otherwise a request arriving during the bulk sync can hold a second
	// SQLite transaction and surface a misleading "database is locked" error.
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	accounts, err := s.store.ListCloudAccounts(ctx, false)
	if err != nil {
		return CloudSyncResult{}, err
	}
	for _, account := range accounts {
		if account.ID == accountID {
			return s.syncAccount(ctx, account), nil
		}
	}
	return CloudSyncResult{}, sql.ErrNoRows
}

func (s *CloudService) SyncInstance(ctx context.Context, instanceID string) (CloudSyncResult, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	account, err := s.store.CloudAccountForInstance(ctx, instanceID)
	if err != nil {
		return CloudSyncResult{}, err
	}
	return s.syncAccount(ctx, account), nil
}

func (s *CloudService) RenameInstance(ctx context.Context, instanceID, name string) error {
	if err := s.store.RenameCloudInstance(ctx, instanceID, name); err != nil {
		return err
	}
	return s.store.AddSystemLog(ctx, "info", "system", fmt.Sprintf("重命名实例: %s", instanceID))
}

func (s *CloudService) ReleaseInstance(ctx context.Context, instanceID string) error {
	account, err := s.store.CloudAccountForInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	client, ok := s.clientFor(account).(deleteInstanceClient)
	if !ok {
		return errors.New("cloud client does not support releasing instances")
	}
	if err := client.DeleteInstance(ctx, instanceID); err != nil {
		return err
	}
	if err := s.store.RemoveCloudInstance(ctx, instanceID); err != nil {
		return err
	}
	return s.store.AddSystemLog(ctx, "info", "system", fmt.Sprintf("释放实例: %s", instanceID))
}

func (s *CloudService) Billing(ctx context.Context, accountID int64) (BillingResponse, error) {
	accounts, err := s.store.ListCloudAccounts(ctx, false)
	if err != nil {
		return BillingResponse{}, err
	}
	var account *CloudAccount
	for index := range accounts {
		if accounts[index].ID == accountID {
			account = &accounts[index]
			break
		}
	}
	if account == nil {
		return BillingResponse{}, sql.ErrNoRows
	}
	if account.SiteType == "china" {
		return BillingResponse{Errors: []string{}, Disabled: true}, nil
	}
	client, ok := s.clientFor(*account).(billingClient)
	if !ok {
		return BillingResponse{}, errors.New("cloud client does not support billing")
	}
	var balance aliyun.AccountBalance
	var bill aliyun.BillOverview
	var balanceErr, billErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); balance, balanceErr = client.GetBalance(ctx) }()
	go func() { defer wg.Done(); bill, billErr = client.GetBillOverview(ctx, "") }()
	wg.Wait()
	response := BillingResponse{Errors: make([]string, 0)}
	if balanceErr != nil {
		response.Errors = append(response.Errors, "余额："+friendlyCloudError(balanceErr))
	} else {
		response.Balance = &balance
	}
	if billErr != nil {
		response.Errors = append(response.Errors, "账单："+friendlyCloudError(billErr))
	} else {
		response.Bill = &bill
	}
	return response, nil
}

func (s *CloudService) tryAutomationCycle(ctx context.Context, now time.Time) bool {
	if !s.automationMu.TryLock() {
		return false
	}
	defer s.automationMu.Unlock()
	s.runAutomationCycleLocked(ctx, now)
	return true
}

func (s *CloudService) runAutomationCycleLocked(ctx context.Context, now time.Time) {
	cycleCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	_, _ = s.store.MarkStaleRelayNodes(cycleCtx, 45*time.Second)
	s.runKeepAlive(cycleCtx)
	for _, minute := range s.scheduledPowerMinutes(now) {
		s.runScheduledPower(cycleCtx, minute)
	}
	if now.Day() == 1 && now.Hour() == 0 && now.Minute() == 1 {
		s.runMonthlyReset(cycleCtx)
	}
	if now.Hour() == 0 && now.Minute() == 0 {
		enabled, _ := s.store.GetSetting(cycleCtx, "tg_daily_report")
		if enabled == "1" {
			_ = s.SendDailyReport(cycleCtx)
		}
	}
}

// scheduledPowerMinutes returns the current minute and, when a previous
// automation cycle was delayed, the small gap since that cycle. The scheduler
// ticker has a one-element channel and may drop a tick while cloud APIs or
// notifications are in flight; replaying a short gap prevents a configured
// start from being silently skipped. Long gaps (for example, a controller
// restart after several hours) intentionally run only the current minute.
func (s *CloudService) scheduledPowerMinutes(now time.Time) []string {
	current := now.Truncate(time.Minute)
	previous := s.lastAutomationAt.Truncate(time.Minute)
	s.lastAutomationAt = now
	if previous.IsZero() || current.Before(previous) {
		return []string{current.Format("15:04")}
	}
	gap := int(current.Sub(previous) / time.Minute)
	if gap <= 0 {
		return []string{current.Format("15:04")}
	}
	if gap > 5 {
		return []string{current.Format("15:04")}
	}
	minutes := make([]string, 0, gap)
	for cursor := previous.Add(time.Minute); !cursor.After(current); cursor = cursor.Add(time.Minute) {
		minutes = append(minutes, cursor.Format("15:04"))
	}
	return minutes
}

func (s *CloudService) runKeepAlive(ctx context.Context) {
	accounts, err := s.store.ListCloudAccounts(ctx, true)
	if err != nil {
		return
	}
	for _, account := range accounts {
		if !account.KeepAlive || account.ProtectedInstanceID == "" || account.ManualStopped {
			continue
		}
		client, ok := s.clientFor(account).(instanceStatusClient)
		if !ok {
			continue
		}
		status, err := client.GetInstanceStatus(ctx, account.ProtectedInstanceID)
		if err != nil {
			_ = s.store.AddSystemLog(ctx, "warning", "keepalive", fmt.Sprintf("[%s] 保活状态检查失败: %s", account.Name, friendlyCloudError(err)))
			continue
		}
		_ = s.store.UpdateCloudInstanceStatus(ctx, account.ProtectedInstanceID, status)
		if !strings.EqualFold(status, "Stopped") {
			continue
		}
		if err := s.clientFor(account).StartInstance(ctx, account.ProtectedInstanceID); err != nil {
			if isNoStockError(err) {
				if !account.NoStockNotified {
					_ = s.store.SetAccountNoStockNotified(ctx, account.ID, true)
					message := fmt.Sprintf("[%s] 保活失败：抢占实例库存不足，系统将持续重试", account.Name)
					_ = s.store.AddSystemLog(ctx, "warning", "keepalive", message)
					_ = s.sendTelegram(ctx, message)
				}
				continue
			}
			_ = s.store.AddSystemLog(ctx, "warning", "keepalive", fmt.Sprintf("[%s] 保活启动失败: %s", account.Name, friendlyCloudError(err)))
			continue
		}
		s.reconcilePowerState(ctx, account.ProtectedInstanceID, "Running")
		if account.NoStockNotified {
			_ = s.store.SetAccountNoStockNotified(ctx, account.ID, false)
			_ = s.sendTelegram(ctx, fmt.Sprintf("[%s] 抢占实例库存已恢复，实例已重新启动", account.Name))
		}
		message := fmt.Sprintf("[%s] 实例 %s 被回收，已自动拉起", account.Name, account.ProtectedInstanceID)
		_ = s.store.AddSystemLog(ctx, "info", "keepalive", message)
		_ = s.sendTelegram(ctx, message)
	}
}

func (s *CloudService) runScheduledPower(ctx context.Context, hhmm string) {
	accounts, err := s.store.ListCloudAccounts(ctx, true)
	if err != nil {
		return
	}
	for _, account := range accounts {
		if account.ProtectedInstanceID == "" {
			continue
		}
		// The local instance projection makes repeated/delayed scheduler ticks
		// idempotent while still allowing a configured start to recover an ECS
		// that was stopped outside the panel (manual_stopped may be false there).
		instanceStatus, _ := s.store.CloudInstanceStatus(ctx, account.ProtectedInstanceID)
		stoppedThisCycle := false
		if account.AutoStopTime == hhmm && !account.ManualStopped && !strings.EqualFold(instanceStatus, "Stopped") {
			err := s.clientFor(account).StopInstance(ctx, account.ProtectedInstanceID, account.ShutdownMode)
			if err == nil {
				stoppedThisCycle = true
				_ = s.store.SetAccountManualStopped(ctx, account.ID, true)
				s.reconcilePowerState(ctx, account.ProtectedInstanceID, "Stopped")
				message := fmt.Sprintf("[%s] 定时关机已执行 %s", account.Name, hhmm)
				_ = s.store.AddSystemLog(ctx, "info", "scheduler", message)
				_ = s.sendTelegram(ctx, message)
			} else {
				_ = s.store.AddSystemLog(ctx, "error", "scheduler", fmt.Sprintf("[%s] 定时关机失败: %s", account.Name, friendlyCloudError(err)))
			}
		}
		if account.AutoStartTime == hhmm && !stoppedThisCycle && (account.ManualStopped || !strings.EqualFold(instanceStatus, "Running")) {
			err := s.clientFor(account).StartInstance(ctx, account.ProtectedInstanceID)
			if err == nil {
				_ = s.store.SetAccountManualStopped(ctx, account.ID, false)
				s.reconcilePowerState(ctx, account.ProtectedInstanceID, "Running")
				message := fmt.Sprintf("[%s] 定时开机已执行 %s", account.Name, hhmm)
				_ = s.store.AddSystemLog(ctx, "info", "scheduler", message)
				_ = s.sendTelegram(ctx, message)
			} else {
				_ = s.store.AddSystemLog(ctx, "error", "scheduler", fmt.Sprintf("[%s] 定时开机失败: %s", account.Name, friendlyCloudError(err)))
			}
		}
	}
}

// reconcilePowerState keeps the local projection and entry-pool desired state
// aligned with an accepted ECS power command. ECS power APIs are asynchronous,
// so the next cloud inventory sync remains authoritative for the final status
// and any newly assigned public IP. Marking a stopped host offline immediately
// prevents a stale Relay address from being served during that transition;
// the Agent heartbeat promotes it back after boot.
func (s *CloudService) reconcilePowerState(ctx context.Context, instanceID, status string) {
	_ = s.store.UpdateCloudInstanceStatus(ctx, instanceID, status)
	if strings.EqualFold(status, "Stopped") {
		_ = s.store.MarkRelayNodesForInstance(ctx, instanceID, "offline")
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = s.store.RefreshRelayAgentDNSRecords(refreshCtx)
	_ = s.store.RefreshAllRelayPoolDNS(refreshCtx)
}

func (s *CloudService) runMonthlyReset(ctx context.Context) {
	accounts, err := s.store.ListCloudAccounts(ctx, true)
	if err != nil {
		return
	}
	restarted := make([]string, 0)
	for _, account := range accounts {
		if !account.ManualStopped {
			continue
		}
		if account.ProtectedInstanceID != "" {
			if err := s.clientFor(account).StartInstance(ctx, account.ProtectedInstanceID); err != nil {
				continue
			}
			s.reconcilePowerState(ctx, account.ProtectedInstanceID, "Running")
		}
		_ = s.store.SetAccountManualStopped(ctx, account.ID, false)
		_ = s.store.SetAccountNoStockNotified(ctx, account.ID, false)
		restarted = append(restarted, account.Name)
	}
	if len(restarted) > 0 {
		message := "每月流量周期已重置，已恢复并启动：" + strings.Join(restarted, "、")
		_ = s.store.AddSystemLog(ctx, "info", "system", message)
		_ = s.sendTelegram(ctx, message)
	}
}

func (s *CloudService) SendDailyReport(ctx context.Context) error {
	overview, err := s.store.CloudOverview(ctx)
	if err != nil {
		return err
	}
	instances := make(map[int64]CloudInstance)
	for _, instance := range overview.Instances {
		instances[instance.AccountID] = instance
	}
	traffic := make(map[int64]AccountTraffic, len(overview.Traffic))
	for _, snapshot := range overview.Traffic {
		traffic[snapshot.AccountID] = snapshot
	}
	var report strings.Builder
	report.WriteString("AliCDT 每日流量汇报\n")
	report.WriteString(time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04"))
	for _, account := range overview.Accounts {
		report.WriteString("\n\n")
		report.WriteString(account.Name)
		if instance, ok := instances[account.ID]; ok {
			usedGB := 0.0
			if snapshot, exists := traffic[account.ID]; exists {
				usedGB = snapshot.UsedGB
			}
			report.WriteString(fmt.Sprintf("\n状态: %s\n账户流量: %.2f GB / %.2f GB\n地域: %s", instance.Status, usedGB, account.TrafficLimitGB, instance.RegionID))
		} else {
			report.WriteString("\n暂无实例数据")
		}
		if account.KeepAlive && account.NoStockNotified {
			report.WriteString("\n抢占实例库存不足，保活正在持续重试")
		}
	}
	if err := s.sendTelegram(ctx, report.String()); err != nil {
		return err
	}
	return s.store.AddSystemLog(ctx, "info", "system", "每日流量汇报已发送")
}

func (s *CloudService) TestTelegram(ctx context.Context) error {
	return s.sendTelegram(ctx, "AliCDT Manager 通知通道测试成功")
}

func (s *CloudService) sendTelegram(ctx context.Context, message string) error {
	token, err := s.store.GetSetting(ctx, "tg_bot_token")
	if err != nil {
		return err
	}
	chatID, err := s.store.GetSetting(ctx, "tg_chat_id")
	if err != nil {
		return err
	}
	if token == "" || chatID == "" {
		return errors.New("telegram Bot Token and Chat ID are required")
	}
	form := url.Values{"chat_id": {chatID}, "text": {message}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("telegram returned HTTP %d", response.StatusCode)
	}
	return nil
}

func friendlyCloudError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "NoStock"):
		return "该可用区抢占式实例库存不足，请稍后重试"
	case strings.Contains(message, "InvalidAccessKeyId"), strings.Contains(message, "SignatureDoesNotMatch"):
		return "AccessKey 无效或已过期"
	case strings.Contains(strings.ToLower(message), "not authorized"):
		return "账户权限不足"
	case strings.Contains(message, "InstanceNotFound"), strings.Contains(message, "InvalidInstanceId"):
		return "实例不存在或已被释放"
	case strings.Contains(message, "IncorrectInstanceStatus"):
		return "实例当前状态不允许此操作"
	default:
		return message
	}
}

func isNoStockError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NoStock")
}
