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
	store     *Store
	syncMu    sync.Mutex
	clientFor func(CloudAccount) cloudClient
}

type cloudClient interface {
	GetInstances(context.Context) ([]aliyun.Instance, error)
	GetCDTTraffic(context.Context) (float64, error)
	StartInstance(context.Context, string) error
	StopInstance(context.Context, string, string) error
}

type CloudSyncResult struct {
	AccountID           int64   `json:"account_id"`
	AccountName         string  `json:"account_name"`
	InstancesOK         bool    `json:"instances_ok"`
	TrafficOK           bool    `json:"traffic_ok"`
	InstanceCount       int     `json:"instance_count"`
	TrafficGB           float64 `json:"traffic_gb,omitempty"`
	ProtectionMode      string  `json:"protection_mode,omitempty"`
	ProtectionTriggered bool    `json:"protection_triggered,omitempty"`
	ProtectionAction    string  `json:"protection_action,omitempty"`
	Error               string  `json:"error,omitempty"`
}

func NewCloudService(store *Store) *CloudService {
	return &CloudService{
		store: store,
		clientFor: func(account CloudAccount) cloudClient {
			return aliyun.NewClient(account.AccessKeyID, account.AccessKeySecret, account.RegionID, account.SiteType)
		},
	}
}

func (s *CloudService) SyncAll(ctx context.Context) ([]CloudSyncResult, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
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
		protection, protectionErr = s.store.ApplyTrafficProtection(ctx, account.ID, traffic)
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
		}
	}
	result := CloudSyncResult{
		AccountID: account.ID, AccountName: account.Name,
		InstancesOK: instanceErr == nil, TrafficOK: trafficErr == nil,
		InstanceCount: len(instances), TrafficGB: traffic,
		ProtectionMode: protection.Mode, ProtectionTriggered: protection.Triggered, ProtectionAction: protectionAction,
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
	return s.clientFor(account).StartInstance(ctx, instanceID)
}

func (s *CloudService) StopInstance(ctx context.Context, instanceID string) error {
	account, err := s.store.CloudAccountForInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	return s.clientFor(account).StopInstance(ctx, instanceID, account.ShutdownMode)
}

func (s *CloudService) RunScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncCtx, cancel := context.WithTimeout(ctx, time.Minute)
			_, _ = s.SyncAll(syncCtx)
			cancel()
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
