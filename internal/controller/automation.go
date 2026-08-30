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

func (s *CloudService) runAutomationCycle(ctx context.Context, now time.Time) {
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	cycleCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	s.runKeepAlive(cycleCtx)
	s.runScheduledPower(cycleCtx, now.Format("15:04"))
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
		if account.AutoStopTime == hhmm {
			err := s.clientFor(account).StopInstance(ctx, account.ProtectedInstanceID, account.ShutdownMode)
			if err == nil {
				_ = s.store.SetAccountManualStopped(ctx, account.ID, true)
				message := fmt.Sprintf("[%s] 定时关机已执行 %s", account.Name, hhmm)
				_ = s.store.AddSystemLog(ctx, "info", "scheduler", message)
				_ = s.sendTelegram(ctx, message)
			} else {
				_ = s.store.AddSystemLog(ctx, "error", "scheduler", fmt.Sprintf("[%s] 定时关机失败: %s", account.Name, friendlyCloudError(err)))
			}
		}
		if account.AutoStartTime == hhmm {
			err := s.clientFor(account).StartInstance(ctx, account.ProtectedInstanceID)
			if err == nil {
				_ = s.store.SetAccountManualStopped(ctx, account.ID, false)
				message := fmt.Sprintf("[%s] 定时开机已执行 %s", account.Name, hhmm)
				_ = s.store.AddSystemLog(ctx, "info", "scheduler", message)
				_ = s.sendTelegram(ctx, message)
			} else {
				_ = s.store.AddSystemLog(ctx, "error", "scheduler", fmt.Sprintf("[%s] 定时开机失败: %s", account.Name, friendlyCloudError(err)))
			}
		}
	}
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
	var report strings.Builder
	report.WriteString("AliCDT 每日流量汇报\n")
	report.WriteString(time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04"))
	for _, account := range overview.Accounts {
		report.WriteString("\n\n")
		report.WriteString(account.Name)
		if instance, ok := instances[account.ID]; ok {
			report.WriteString(fmt.Sprintf("\n状态: %s\n流量: %.2f GB / %.2f GB\n地域: %s", instance.Status, instance.TrafficUsedGB, account.TrafficLimitGB, instance.RegionID))
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
		return errors.New("Telegram Bot Token and Chat ID are required")
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
		return fmt.Errorf("Telegram returned HTTP %d", response.StatusCode)
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
