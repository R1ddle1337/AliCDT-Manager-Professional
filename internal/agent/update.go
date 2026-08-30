package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

var ErrRestartRequested = errors.New("agent update installed; restart requested")

type pendingUpdate struct {
	BackupPath   string    `json:"backup_path"`
	TargetSHA256 string    `json:"target_sha256"`
	Attempts     int       `json:"attempts"`
	PreparedAt   time.Time `json:"prepared_at"`
}

func (c *Client) updateMarkerPath() string {
	return filepath.Join(c.opts.DataDir, "pending-agent-update.json")
}

func (c *Client) currentUpdateState() (string, string) {
	c.updateMu.RLock()
	defer c.updateMu.RUnlock()
	status := c.updateStatus
	if status == "" {
		status = "idle"
	}
	return status, c.updateError
}

func (c *Client) setUpdateState(status, updateErr string) {
	c.updateMu.Lock()
	c.updateStatus = status
	c.updateError = updateErr
	c.updateMu.Unlock()
}

func (c *Client) checkForUpdate(ctx context.Context) error {
	currentHash, err := fileSHA256(c.opts.BinaryPath)
	if err != nil {
		return fmt.Errorf("hash current agent: %w", err)
	}
	path := fmt.Sprintf("/api/v2/agents/%s/release?arch=%s&sha256=%s", url.PathEscape(c.creds.AgentID), url.QueryEscape(runtime.GOARCH), url.QueryEscape(currentHash))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.opts.ControllerURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.creds.Secret)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("release endpoint returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var release protocol.AgentRelease
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return err
	}
	if !release.Available || release.SHA256 == "" || strings.EqualFold(release.SHA256, currentHash) {
		return nil
	}
	if !validSHA256(release.SHA256) {
		return errors.New("controller returned an invalid agent checksum")
	}
	if err := c.setRemoteUpdateState(ctx, "draining", ""); err != nil {
		return fmt.Errorf("prepare agent drain: %w", err)
	}
	c.setUpdateState("draining", "")
	// Give DNS reconciliation a chance to remove this Relay from new entry
	// connections before replacing the process. Existing connections remain
	// subject to the normal restart boundary.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
	}
	c.setUpdateState("updating", "")
	assetURL, err := c.resolveReleaseURL(release.URL)
	if err != nil {
		return c.updateFailure(ctx, err)
	}
	data, err := c.downloadRelease(ctx, assetURL, release.Size)
	if err != nil {
		return c.updateFailure(ctx, err)
	}
	actual := sha256.Sum256(data)
	actualHash := hex.EncodeToString(actual[:])
	if !strings.EqualFold(actualHash, release.SHA256) {
		return c.updateFailure(ctx, fmt.Errorf("agent checksum mismatch: expected %s, got %s", release.SHA256, actualHash))
	}
	if err := c.installUpdate(data, actualHash); err != nil {
		return c.updateFailure(ctx, err)
	}
	_ = c.setRemoteUpdateState(ctx, "updating", "")
	return ErrRestartRequested
}

func (c *Client) updateFailure(ctx context.Context, err error) error {
	c.setUpdateState("failed", err.Error())
	_ = c.setRemoteUpdateState(ctx, "failed", err.Error())
	return err
}

func (c *Client) setRemoteUpdateState(ctx context.Context, status, updateErr string) error {
	path := fmt.Sprintf("/api/v2/agents/%s/update/state", url.PathEscape(c.creds.AgentID))
	return c.requestJSON(ctx, http.MethodPost, path, c.creds.Secret, map[string]string{"status": status, "error": updateErr}, nil)
}

func (c *Client) resolveReleaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	base, err := url.Parse(c.opts.ControllerURL)
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, base.Scheme) || !strings.EqualFold(parsed.Host, base.Host) {
			return "", errors.New("agent release URL is outside the configured controller")
		}
		return parsed.String(), nil
	}
	if !strings.HasPrefix(parsed.Path, "/agent/") {
		return "", errors.New("agent release URL is invalid")
	}
	return base.ResolveReference(parsed).String(), nil
}

func (c *Client) downloadRelease(ctx context.Context, assetURL string, size int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.creds.Secret)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent asset download returned %s", response.Status)
	}
	maxSize := int64(128 << 20)
	if size > 0 && size < maxSize {
		maxSize = size + 1
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSize))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize-1 {
		return nil, errors.New("agent asset is too large")
	}
	return data, nil
}

func (c *Client) installUpdate(data []byte, targetHash string) error {
	binaryPath := c.opts.BinaryPath
	if strings.TrimSpace(binaryPath) == "" {
		return errors.New("agent executable path is unknown")
	}
	current, err := os.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("read current agent: %w", err)
	}
	backupPath := filepath.Join(c.opts.DataDir, "agent-backup-"+targetHash+".bin")
	if err := writeFileAtomic(backupPath, current, 0700); err != nil {
		return fmt.Errorf("save rollback binary: %w", err)
	}
	marker := pendingUpdate{BackupPath: backupPath, TargetSHA256: strings.ToLower(targetHash), PreparedAt: time.Now().UTC()}
	encoded, _ := json.MarshalIndent(marker, "", "  ")
	if err := writeFileAtomic(c.updateMarkerPath(), encoded, 0600); err != nil {
		return fmt.Errorf("write update marker: %w", err)
	}
	replaced := true
	if err := writeFileAtomic(binaryPath, data, 0755); err != nil {
		if fallbackErr := c.installViaSystemd(data, targetHash); fallbackErr != nil {
			return fmt.Errorf("replace agent binary: %w (systemd fallback: %v)", err, fallbackErr)
		}
		replaced = false
	}
	if !replaced {
		return nil
	}
	verified, err := fileSHA256(binaryPath)
	if err != nil || !strings.EqualFold(verified, targetHash) {
		if err == nil {
			err = errors.New("post-install checksum mismatch")
		}
		return err
	}
	return nil
}

func (c *Client) installViaSystemd(data []byte, targetHash string) error {
	payload := filepath.Join(c.opts.DataDir, "agent-update-"+strings.ToLower(targetHash)+".bin")
	if err := writeFileAtomic(payload, data, 0700); err != nil {
		return err
	}
	binaryPath := shellQuote(c.opts.BinaryPath)
	payloadPath := shellQuote(payload)
	serviceName := shellQuote(c.opts.ServiceName)
	script := fmt.Sprintf("set -eu; tmp=%s.autoupdate.$$; install -m 0755 %s \"$tmp\"; mv -f \"$tmp\" %s; rm -f %s; systemctl restart %s", binaryPath, payloadPath, binaryPath, payloadPath, serviceName)
	unit := "cdt-relay-agent-updater-" + strings.ToLower(targetHash[:12])
	command := exec.Command("systemd-run", "--unit="+unit, "--collect", "/bin/sh", "-c", script)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("systemd-run: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func (c *Client) recoverPendingUpdate() error {
	data, err := os.ReadFile(c.updateMarkerPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var marker pendingUpdate
	if err := json.Unmarshal(data, &marker); err != nil {
		return fmt.Errorf("parse update marker: %w", err)
	}
	currentHash, hashErr := fileSHA256(c.opts.BinaryPath)
	// Count every startup while the marker is present. A binary can have the
	// expected checksum yet still fail before sending its first heartbeat; in
	// that case repeatedly accepting the marker would make rollback impossible.
	marker.Attempts++
	if marker.Attempts >= 3 {
		backup, readErr := os.ReadFile(marker.BackupPath)
		if readErr != nil {
			return fmt.Errorf("read rollback binary: %w", readErr)
		}
		if err := writeFileAtomic(c.opts.BinaryPath, backup, 0755); err != nil {
			return fmt.Errorf("rollback agent binary: %w", err)
		}
		_ = os.Remove(marker.BackupPath)
		_ = os.Remove(c.updateMarkerPath())
		c.setUpdateState("failed", "agent update failed repeatedly; rolled back")
		return nil
	}
	if hashErr != nil {
		c.setUpdateState("failed", "updated agent binary could not be inspected")
	} else if !strings.EqualFold(currentHash, marker.TargetSHA256) {
		c.setUpdateState("failed", "agent update did not start successfully")
	} else {
		c.setUpdateState("updating", "")
	}
	encoded, _ := json.MarshalIndent(marker, "", "  ")
	if err := writeFileAtomic(c.updateMarkerPath(), encoded, 0600); err != nil {
		return err
	}
	return nil
}

func (c *Client) confirmPendingUpdate() error {
	data, err := os.ReadFile(c.updateMarkerPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var marker pendingUpdate
	if err := json.Unmarshal(data, &marker); err != nil {
		return err
	}
	currentHash, err := fileSHA256(c.opts.BinaryPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(currentHash, marker.TargetSHA256) {
		return nil
	}
	_ = os.Remove(marker.BackupPath)
	_ = os.Remove(c.updateMarkerPath())
	c.setUpdateState("idle", "")
	_ = c.setRemoteUpdateState(context.Background(), "idle", "")
	return nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func validSHA256(value string) bool {
	if len(strings.TrimSpace(value)) != 64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil
}
