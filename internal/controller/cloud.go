package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/aliyun"
)

type CloudService struct {
	store               *Store
	syncMu              sync.Mutex
	automationMu        sync.Mutex
	lastAutomationAt    time.Time
	clientFor           func(CloudAccount) cloudClient
	trafficSafetyWindow time.Duration
}

type cloudClient interface {
	GetInstances(context.Context) ([]aliyun.Instance, error)
	GetCDTTraffic(context.Context) (float64, error)
	StartInstance(context.Context, string) error
	StopInstance(context.Context, string, string) error
}

type CloudSyncResult struct {
	AccountID              int64   `json:"account_id"`
	AccountName            string  `json:"account_name"`
	InstancesOK            bool    `json:"instances_ok"`
	TrafficOK              bool    `json:"traffic_ok"`
	InstanceCount          int     `json:"instance_count"`
	TrafficGB              float64 `json:"traffic_gb,omitempty"`
	TrafficRateGBPerMinute float64 `json:"traffic_rate_gb_per_minute,omitempty"`
	TrafficProjectedGB     float64 `json:"traffic_projected_gb,omitempty"`
	ProtectionMode         string  `json:"protection_mode,omitempty"`
	ProtectionTriggered    bool    `json:"protection_triggered,omitempty"`
	ProtectionPredictive   bool    `json:"protection_predictive,omitempty"`
	ProtectionAction       string  `json:"protection_action,omitempty"`
	Error                  string  `json:"error,omitempty"`
}

const defaultTrafficSafetyWindow = 4 * time.Minute

func NewCloudService(store *Store) *CloudService {
	return &CloudService{
		store:               store,
		trafficSafetyWindow: defaultTrafficSafetyWindow,
		clientFor: func(account CloudAccount) cloudClient {
			return aliyun.NewClient(account.AccessKeyID, account.AccessKeySecret, account.RegionID, account.SiteType)
		},
	}
}

func (s *CloudService) SetTrafficSafetyWindow(window time.Duration) {
	if window < 0 {
		window = 0
	}
	s.trafficSafetyWindow = window
}

func (s *CloudService) SyncAll(ctx context.Context) ([]CloudSyncResult, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncAll(ctx)
}

func (s *CloudService) syncAll(ctx context.Context) ([]CloudSyncResult, error) {
	accounts, err := s.store.ListCloudAccounts(ctx, true)
	if err != nil {
		return nil, err
	}
	results := make([]CloudSyncResult, len(accounts))
	semaphore := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for index, account := range accounts {
		index, account := index, account
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = CloudSyncResult{AccountID: account.ID, AccountName: account.Name, Error: ctx.Err().Error()}
				return
			}
			results[index] = s.syncAccount(ctx, account)
		}()
	}
	wg.Wait()
	return results, nil
}

func (s *CloudService) trySyncAll(ctx context.Context) bool {
	if !s.syncMu.TryLock() {
		return false
	}
	defer s.syncMu.Unlock()
	_, _ = s.syncAll(ctx)
	return true
}

func (s *CloudService) syncAccount(ctx context.Context, account CloudAccount) CloudSyncResult {
	client := s.clientFor(account)
	var instances []aliyun.Instance
	var traffic float64
	var instanceErr, trafficErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		instances, instanceErr = client.GetInstances(ctx)
	}()
	go func() {
		defer wg.Done()
		traffic, trafficErr = client.GetCDTTraffic(ctx)
	}()
	wg.Wait()

	updates := make([]CloudInstanceUpdate, 0, len(instances))
	for _, instance := range instances {
		updates = append(updates, CloudInstanceUpdate{
			InstanceID: instance.InstanceID, InstanceName: instance.InstanceName,
			RegionID: instance.RegionID, Status: instance.Status, PublicIP: instance.PublicIP,
			InstanceType: instance.InstanceType, BandwidthMbps: instance.BandwidthMbps, IsSpot: instance.IsSpot,
		})
	}
	instanceError, trafficError := errorString(instanceErr), errorString(trafficErr)
	if err := s.store.SaveCloudSync(ctx, account, updates, instanceErr == nil, instanceError, traffic, trafficErr == nil, trafficError); err != nil {
		return CloudSyncResult{AccountID: account.ID, AccountName: account.Name, Error: err.Error()}
	}
	var protection TrafficProtectionDecision
	var protectionErr error
	protectionAction := ""
	if trafficErr == nil {
		protection, protectionErr = s.store.ApplyTrafficProtectionWithWindow(ctx, account.ID, traffic, s.trafficSafetyWindow)
		if protectionErr == nil && protection.NeedsStop {
			authorized, authorizeErr := s.store.ConfirmTrafficProtectionStop(ctx, account.ID, protection.InstanceID)
			switch {
			case authorizeErr != nil:
				protectionErr = fmt.Errorf("confirm traffic protection stop: %w", authorizeErr)
			case !authorized:
				protectionAction = "stop_ecs_cancelled"
			default:
				alreadyStopped := false
				for _, instance := range instances {
					if instance.InstanceID == protection.InstanceID && strings.EqualFold(instance.Status, "Stopped") {
						alreadyStopped = true
						break
					}
				}
				var stopErr error
				if !alreadyStopped {
					stopErr = client.StopInstance(ctx, protection.InstanceID, account.ShutdownMode)
				}
				markErr := s.store.MarkTrafficProtectionAction(ctx, account.ID, stopErr)
				switch {
				case stopErr != nil:
					protectionErr = fmt.Errorf("traffic protection stop ECS: %w", stopErr)
				case markErr != nil:
					protectionErr = fmt.Errorf("record traffic protection action: %w", markErr)
				default:
					if alreadyStopped {
						protectionAction = "stop_ecs_already_stopped"
					} else {
						protectionAction = "stop_ecs_sent"
					}
				}
			}
		} else if protectionErr == nil && protection.Changed {
			protectionAction = protection.Mode
			if protection.Predictive {
				protectionAction += "_predictive"
			}
		}
	}
	// Keep the controller-side DNS desired state in lockstep with a single
	// account sync as well as the bulk sync path. The provider API itself is
	// reconciled by RunDNSScheduler, so a transient provider error cannot undo
	// the protection decision.
	refreshCtx, refreshCancel := context.WithTimeout(ctx, 10*time.Second)
	_ = s.store.RefreshRelayAgentDNSRecords(refreshCtx)
	_ = s.store.RefreshAllRelayPoolDNS(refreshCtx)
	refreshCancel()
	result := CloudSyncResult{
		AccountID: account.ID, AccountName: account.Name,
		InstancesOK: instanceErr == nil, TrafficOK: trafficErr == nil,
		InstanceCount: len(instances), TrafficGB: traffic,
		TrafficRateGBPerMinute: protection.RateGBPerMinute, TrafficProjectedGB: protection.ProjectedGB,
		ProtectionMode: protection.Mode, ProtectionTriggered: protection.Triggered, ProtectionPredictive: protection.Predictive, ProtectionAction: protectionAction,
	}
	if instanceErr != nil || trafficErr != nil || protectionErr != nil {
		result.Error = stringsJoinErrors(instanceError, trafficError, errorString(protectionErr))
	}
	return result
}

func (s *CloudService) StartInstance(ctx context.Context, instanceID string) error {
	account, err := s.store.CloudAccountForInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	if err := s.clientFor(account).StartInstance(ctx, instanceID); err != nil {
		return err
	}
	if err := s.store.SetAccountManualStopped(ctx, account.ID, false); err != nil {
		return err
	}
	s.reconcilePowerState(ctx, instanceID, "Running")
	_ = s.store.AddSystemLog(ctx, "info", "system", fmt.Sprintf("手动开机: %s", instanceID))
	return nil
}

func (s *CloudService) StopInstance(ctx context.Context, instanceID string) error {
	account, err := s.store.CloudAccountForInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	if err := s.clientFor(account).StopInstance(ctx, instanceID, account.ShutdownMode); err != nil {
		return err
	}
	if err := s.store.SetAccountManualStopped(ctx, account.ID, true); err != nil {
		return err
	}
	s.reconcilePowerState(ctx, instanceID, "Stopped")
	_ = s.store.AddSystemLog(ctx, "info", "system", fmt.Sprintf("手动关机: %s", instanceID))
	return nil
}

func (s *CloudService) RunScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	syncTicker := time.NewTicker(interval)
	automationTicker := time.NewTicker(time.Minute)
	defer syncTicker.Stop()
	defer automationTicker.Stop()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-syncTicker.C:
			go func() {
				syncCtx, cancel := context.WithTimeout(ctx, time.Minute)
				defer cancel()
				s.trySyncAll(syncCtx)
			}()
		case tick := <-automationTicker.C:
			go s.tryAutomationCycle(ctx, tick.In(location))
		}
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func stringsJoinErrors(instanceError, trafficError, protectionError string) string {
	parts := make([]string, 0, 3)
	if instanceError != "" {
		parts = append(parts, "ECS: "+instanceError)
	}
	if trafficError != "" {
		parts = append(parts, "CDT: "+trafficError)
	}
	if protectionError != "" {
		parts = append(parts, "Protection: "+protectionError)
	}
	return strings.Join(parts, "; ")
}
