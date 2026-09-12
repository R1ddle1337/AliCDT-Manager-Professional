package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	for _, minute := range s.scheduledPowerMinutes(now) {
		// Evaluate each replayed minute against itself. Using the latest tick
		// for every replay could cross the start boundary and wake an instance
		// one minute early when a cloud call delayed the scheduler.
		s.runScheduledPowerAt(cycleCtx, minute, minute)
	}
	s.runKeepAliveAt(cycleCtx, now.Format("15:04"))
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

// inScheduledDowntime supports both overnight and same-day schedules.
// Equal or incomplete schedules do not define an interval.
func inScheduledDowntime(account CloudAccount, hhmm string) bool {
	stop, start := account.AutoStopTime, account.AutoStartTime
	if stop == "" || start == "" || stop == start {
		return false
	}
	if stop < start {
		return hhmm >= stop && hhmm < start
	}
	return hhmm >= stop || hhmm < start
}

func (s *CloudService) runKeepAlive(ctx context.Context) {
	s.runKeepAliveAt(ctx, time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("15:04"))
}

func (s *CloudService) runKeepAliveAt(ctx context.Context, hhmm string) {
	accounts, err := s.store.ListCloudAccounts(ctx, true)
	if err != nil {
		return
	}
	for _, account := range accounts {
		if !account.KeepAlive || account.ProtectedInstanceID == "" || account.PowerStopReason != "" || inScheduledDowntime(account, hhmm) || account.AutoStartTime == hhmm {
			continue
		}
		status, err := s.currentInstanceStatus(ctx, account, account.ProtectedInstanceID)
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
	s.runScheduledPowerAt(ctx, hhmm, hhmm)
}

func (s *CloudService) runScheduledPowerAt(ctx context.Context, scheduledMinute, currentMinute string) {
	accounts, err := s.store.ListCloudAccounts(ctx, true)
	if err != nil {
		return
	}
	for _, account := range accounts {
		if account.ProtectedInstanceID == "" || account.PowerStopReason == "manual" || account.PowerStopReason == "protection" {
			continue
		}
		paired := account.AutoStartTime != "" && account.AutoStopTime != "" && account.AutoStartTime != account.AutoStopTime
		downtime := inScheduledDowntime(account, currentMinute)
		stopDue := account.AutoStopTime == scheduledMinute && (!paired || downtime)
		startDue := account.AutoStartTime == scheduledMinute && (!paired || !downtime)
		// Persist intent before calling ECS so a timeout or controller restart
		// cannot lose the scheduled operation. Paired schedules retry only in
		// the appropriate interval, independently of the keep-alive toggle.
		if account.PowerStopReason == "scheduled" {
			if paired {
				stopDue, startDue = downtime, !downtime
			} else if account.AutoStartTime != "" && currentMinute >= account.AutoStartTime {
				startDue = true
			}
		}
		if !stopDue && !startDue {
			continue
		}
		if account.PowerStopReason == "" {
			if err := s.store.SetAccountPowerStopReason(ctx, account.ID, "scheduled"); err != nil {
				continue
			}
		}
		status, err := s.currentInstanceStatus(ctx, account, account.ProtectedInstanceID)
		if err != nil {
			message := strings.ToLower(err.Error())
			if strings.Contains(message, "not found") || strings.Contains(message, "does not exist") || strings.Contains(message, "released") {
				// Do not keep a released instance in the scheduled-stop state;
				// that would cause a warning and API lookup every minute forever.
				_ = s.store.SetAccountPowerStopReason(ctx, account.ID, "")
				_ = s.store.AddSystemLog(ctx, "warning", "scheduler", fmt.Sprintf("[%s] 绑定 ECS 已不存在，请重新选择实例；已暂停该账户的定时电源任务", account.Name))
				continue
			}
			_ = s.store.AddSystemLog(ctx, "warning", "scheduler", fmt.Sprintf("[%s] 定时电源任务等待有效实例状态，下次重试", account.Name))
			continue
		}
		if status != "Running" && status != "Stopped" && status != "Starting" && status != "Stopping" {
			_ = s.store.AddSystemLog(ctx, "warning", "scheduler", fmt.Sprintf("[%s] 定时电源任务等待有效实例状态，下次重试", account.Name))
			continue
		}
		if stopDue {
			if status != "Running" {
				continue
			}
			shutdownMode := account.ShutdownMode
			if strings.EqualFold(shutdownMode, "StopCharging") {
				// A spot ECS can be permanently reclaimed while its compute
				// resources are released. That breaks the promise of an automatic
				// start and leaves the account bound to a missing instance. Keep
				// spot resources for scheduled cycles; operators can still choose
				// economical stops manually when a deliberate release is wanted.
				if spot, spotErr := s.store.CloudInstanceIsSpot(ctx, account.ProtectedInstanceID); spotErr != nil || spot {
					shutdownMode = "KeepCharging"
					_ = s.store.AddSystemLog(ctx, "warning", "scheduler", fmt.Sprintf("[%s] 抢占式 ECS 为保证定时开机和 Agent 恢复，定时关机改用普通停机", account.Name))
				}
			}
			if err := s.clientFor(account).StopInstance(ctx, account.ProtectedInstanceID, shutdownMode); err != nil {
				_ = s.store.AddSystemLog(ctx, "error", "scheduler", fmt.Sprintf("[%s] 定时关机失败，下次重试: %s", account.Name, friendlyCloudError(err)))
				continue
			}
			s.reconcilePowerState(ctx, account.ProtectedInstanceID, "Stopped")
			message := fmt.Sprintf("[%s] 定时关机指令已接受（计划 %s，执行 %s，北京时间，%s）", account.Name, account.AutoStopTime, currentMinute, shutdownMode)
			_ = s.store.AddSystemLog(ctx, "info", "scheduler", message)
			_ = s.sendTelegram(ctx, message)
			continue
		}
		if status == "Running" {
			_ = s.store.SetAccountPowerStopReason(ctx, account.ID, "")
			s.reconcilePowerState(ctx, account.ProtectedInstanceID, "Running")
			continue
		}
		if status != "Stopped" {
			continue
		}
		if err := s.clientFor(account).StartInstance(ctx, account.ProtectedInstanceID); err != nil {
			_ = s.store.AddSystemLog(ctx, "error", "scheduler", fmt.Sprintf("[%s] 定时开机失败，将在使用时段重试: %s", account.Name, friendlyCloudError(err)))
			continue
		}
		// Keep the local projection usable immediately. The next inventory sync
		// remains authoritative for the final ECS state and any changed IP.
		_ = s.store.SetAccountPowerStopReason(ctx, account.ID, "")
		s.reconcilePowerState(ctx, account.ProtectedInstanceID, "Running")
		message := fmt.Sprintf("[%s] 定时开机指令已接受（计划 %s，执行 %s，北京时间），等待 ECS 和 Agent 恢复", account.Name, account.AutoStartTime, currentMinute)
		_ = s.store.AddSystemLog(ctx, "info", "scheduler", message)
		_ = s.sendTelegram(ctx, message)
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
		if account.PowerStopReason != "protection" {
			continue
		}
		if account.ProtectedInstanceID != "" {
			if err := s.clientFor(account).StartInstance(ctx, account.ProtectedInstanceID); err != nil {
				continue
			}
			s.reconcilePowerState(ctx, account.ProtectedInstanceID, "Running")
		}
		_ = s.store.SetAccountPowerStopReason(ctx, account.ID, "")
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
	return s.sendDailyReport(ctx, false)
}

func (s *CloudService) TestDailyReport(ctx context.Context) error {
	return s.sendDailyReport(ctx, true)
}

func (s *CloudService) sendDailyReport(ctx context.Context, force bool) error {
	if !force {
		enabled, err := s.telegramNotificationsEnabled(ctx)
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
	}
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
	if err := s.sendTelegramWithOptions(ctx, report.String(), force); err != nil {
		return err
	}
	return s.store.AddSystemLog(ctx, "info", "system", "每日流量汇报已发送")
}

func (s *CloudService) TestTelegram(ctx context.Context) error {
	return s.sendTelegramWithOptions(ctx, "AliCDT Manager 通知通道测试成功", true)
}

func (s *CloudService) sendTelegram(ctx context.Context, message string) error {
	return s.sendTelegramWithOptions(ctx, message, false)
}

func (s *CloudService) sendTelegramWithOptions(ctx context.Context, message string, force bool) error {
	if !force {
		enabled, err := s.telegramNotificationsEnabled(ctx)
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
	}
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
	client := s.telegramHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	for _, chunk := range telegramChunks(message, 3900) {
		if err := s.sendTelegramChunk(ctx, client, token, chatID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *CloudService) telegramNotificationsEnabled(ctx context.Context) (bool, error) {
	enabled, err := s.store.GetSetting(ctx, "tg_enabled")
	if err != nil {
		return false, err
	}
	return enabled != "0", nil
}

func (s *CloudService) sendTelegramChunk(ctx context.Context, client *http.Client, token, chatID, message string) error {
	form := url.Values{"chat_id": {chatID}, "text": {message}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("could not build telegram request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return errors.New("telegram request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("telegram returned HTTP %d", response.StatusCode)
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil || !result.OK {
		return errors.New("telegram rejected the message")
	}
	return nil
}

func telegramChunks(message string, maxRunes int) []string {
	if maxRunes < 1 {
		maxRunes = 3900
	}
	runes := []rune(message)
	if len(runes) == 0 {
		return []string{""}
	}
	chunks := make([]string, 0, (len(runes)+maxRunes-1)/maxRunes)
	for start := 0; start < len(runes); start += maxRunes {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
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
