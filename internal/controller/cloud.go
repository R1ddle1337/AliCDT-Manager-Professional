package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/aliyun"
)

type CloudService struct {
	store *Store
}

type CloudSyncResult struct {
	AccountID     int64   `json:"account_id"`
	AccountName   string  `json:"account_name"`
	InstancesOK   bool    `json:"instances_ok"`
	TrafficOK     bool    `json:"traffic_ok"`
	InstanceCount int     `json:"instance_count"`
	TrafficGB     float64 `json:"traffic_gb,omitempty"`
	Error         string  `json:"error,omitempty"`
}

func NewCloudService(store *Store) *CloudService {
	return &CloudService{store: store}
}

func (s *CloudService) SyncAll(ctx context.Context) ([]CloudSyncResult, error) {
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
	client := aliyun.NewClient(account.AccessKeyID, account.AccessKeySecret, account.RegionID, account.SiteType)
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
	result := CloudSyncResult{
		AccountID: account.ID, AccountName: account.Name,
		InstancesOK: instanceErr == nil, TrafficOK: trafficErr == nil,
		InstanceCount: len(instances), TrafficGB: traffic,
	}
	if instanceErr != nil || trafficErr != nil {
		result.Error = stringsJoinErrors(instanceError, trafficError)
	}
	return result
}

func (s *CloudService) StartInstance(ctx context.Context, instanceID string) error {
	account, err := s.store.CloudAccountForInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	return aliyun.NewClient(account.AccessKeyID, account.AccessKeySecret, account.RegionID, account.SiteType).StartInstance(ctx, instanceID)
}

func (s *CloudService) StopInstance(ctx context.Context, instanceID string) error {
	account, err := s.store.CloudAccountForInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	return aliyun.NewClient(account.AccessKeyID, account.AccessKeySecret, account.RegionID, account.SiteType).StopInstance(ctx, instanceID, account.ShutdownMode)
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

func stringsJoinErrors(instanceError, trafficError string) string {
	switch {
	case instanceError != "" && trafficError != "":
		return fmt.Sprintf("ECS: %s; CDT: %s", instanceError, trafficError)
	case instanceError != "":
		return "ECS: " + instanceError
	default:
		return "CDT: " + trafficError
	}
}
