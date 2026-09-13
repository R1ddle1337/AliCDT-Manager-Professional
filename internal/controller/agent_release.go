package controller

import (
	"bufio"
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
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

const agentReleaseRefreshInterval = 10 * time.Minute

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// agentRelease prepares a release from the configured source. GitHub is the
// authoritative source in production, while the last verified cache and the
// binary shipped in the controller image remain available during outages.
func (s *Server) agentRelease(w http.ResponseWriter, r *http.Request) {
	arch := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("arch")))
	if arch == "" {
		arch = runtime.GOARCH
	}
	if arch != "amd64" && arch != "arm64" {
		writeError(w, http.StatusBadRequest, errors.New("unsupported agent architecture"))
		return
	}
	_ = s.refreshAgentRelease(r.Context())
	asset := "cdt-relay-agent-linux-" + arch
	assetPath, sourceErr := s.agentAssetPath(asset)
	if sourceErr != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(assetPath)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	checksum, err := releaseChecksum(filepath.Join(filepath.Dir(assetPath), "checksums.txt"), asset)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	current := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sha256")))
	version := s.agentVersion
	s.agentReleaseMu.Lock()
	if s.agentReleaseVersion != "" {
		version = s.agentReleaseVersion
	}
	s.agentReleaseMu.Unlock()
	writeJSON(w, http.StatusOK, protocol.AgentRelease{Available: current == "" || current != checksum, Version: version, Architecture: arch, SHA256: checksum, URL: "/agent/" + asset, Size: info.Size()})
}

func (s *Server) agentAssetPath(asset string) (string, error) {
	if !validAgentAsset(asset) {
		return "", errors.New("unsupported agent asset")
	}
	s.agentReleaseMu.Lock()
	releaseSource := s.agentReleaseSource
	cacheDir := s.agentReleaseCacheDir
	assetsDir := s.agentAssetsDir
	s.agentReleaseMu.Unlock()
	if releaseSource == "embedded" && assetsDir != "" {
		candidate := filepath.Join(assetsDir, asset)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if cacheDir != "" {
		candidate := filepath.Join(cacheDir, asset)
		checksumPath := filepath.Join(cacheDir, "checksums.txt")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if checksumInfo, checksumErr := os.Stat(checksumPath); checksumErr == nil && !checksumInfo.IsDir() {
				return candidate, nil
			}
		}
	}
	if assetsDir == "" {
		return "", os.ErrNotExist
	}
	candidate := filepath.Join(assetsDir, asset)
	if info, err := os.Stat(candidate); err != nil || info.IsDir() {
		return "", os.ErrNotExist
	}
	return candidate, nil
}

func validAgentAsset(asset string) bool {
	return asset == "cdt-relay-agent-linux-amd64" || asset == "cdt-relay-agent-linux-arm64" || validDispatcherAsset(asset)
}

func validDispatcherAsset(asset string) bool {
	return asset == "cdt-dispatcher-linux-amd64" || asset == "cdt-dispatcher-linux-arm64"
}

func (s *Server) refreshAgentRelease(ctx context.Context) error {
	s.agentReleaseMu.Lock()
	defer s.agentReleaseMu.Unlock()
	if strings.ToLower(s.agentReleaseSource) != "github" {
		return nil
	}
	if !s.agentReleaseCheckedAt.IsZero() && time.Since(s.agentReleaseCheckedAt) < agentReleaseRefreshInterval {
		return s.agentReleaseErr
	}
	repo, channel, cacheDir := s.agentReleaseRepo, s.agentReleaseChannel, s.agentReleaseCacheDir
	err := downloadGitHubAgentRelease(ctx, repo, channel, cacheDir)
	s.agentReleaseCheckedAt = time.Now()
	s.agentReleaseErr = err
	if err == nil {
		if metadata, readErr := os.ReadFile(filepath.Join(cacheDir, "version")); readErr == nil {
			s.agentReleaseVersion = strings.TrimSpace(string(metadata))
		}
	}
	return err
}

func downloadGitHubAgentRelease(ctx context.Context, repo, channel, cacheDir string) error {
	repo = strings.Trim(repo, "/")
	if repo == "" || strings.Contains(repo, "..") {
		return errors.New("invalid GitHub agent release repository")
	}
	endpoint := "https://api.github.com/repos/" + repo + "/releases/latest"
	if channel != "" && !strings.EqualFold(channel, "latest") {
		endpoint = "https://api.github.com/repos/" + repo + "/releases/tags/" + url.PathEscape(channel)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	release, err := fetchGitHubRelease(ctx, client, endpoint, strings.EqualFold(channel, "latest"))
	if err != nil {
		return err
	}
	assets := make(map[string]string, len(release.Assets))
	for _, asset := range release.Assets {
		if asset.Name != "" && asset.BrowserDownloadURL != "" {
			assets[asset.Name] = asset.BrowserDownloadURL
		}
	}
	checksumURL, ok := assets["checksums.txt"]
	if !ok {
		return errors.New("GitHub release does not contain checksums.txt")
	}
	checksums, err := downloadReleaseFile(ctx, client, checksumURL, 1<<20)
	if err != nil {
		return fmt.Errorf("download release checksums: %w", err)
	}
	parentDir := filepath.Dir(cacheDir)
	if err := os.MkdirAll(parentDir, 0700); err != nil {
		return err
	}
	stagingDir, err := os.MkdirTemp(parentDir, ".agent-release-stage-")
	if err != nil {
		return err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	for _, arch := range []string{"amd64", "arm64"} {
		asset := "cdt-relay-agent-linux-" + arch
		assetURL, ok := assets[asset]
		if !ok {
			return fmt.Errorf("GitHub release does not contain %s", asset)
		}
		data, err := downloadReleaseFile(ctx, client, assetURL, 128<<20)
		if err != nil {
			return fmt.Errorf("download %s: %w", asset, err)
		}
		expected, err := releaseChecksumBytes(checksums, asset)
		if err != nil {
			return err
		}
		if actual := sha256Hex(data); !strings.EqualFold(actual, expected) {
			return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", asset, expected, actual)
		}
		if err := writeReleaseFile(stagingDir, asset, data, 0755); err != nil {
			return err
		}
	}
	if err := writeReleaseFile(stagingDir, "checksums.txt", checksums, 0600); err != nil {
		return err
	}
	if err := writeReleaseFile(stagingDir, "version", []byte(strings.TrimSpace(release.TagName)+"\n"), 0600); err != nil {
		return err
	}
	backupDir := cacheDir + ".previous"
	_ = os.RemoveAll(backupDir)
	if _, err := os.Stat(cacheDir); err == nil {
		if err := os.Rename(cacheDir, backupDir); err != nil {
			return err
		}
	}
	if err := os.Rename(stagingDir, cacheDir); err != nil {
		_ = os.Rename(backupDir, cacheDir)
		return err
	}
	keepStage = true
	_ = os.RemoveAll(backupDir)
	return nil
}

func fetchGitHubRelease(ctx context.Context, client *http.Client, endpoint string, allowPrereleaseFallback bool) (githubRelease, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "AliCDT-Manager-Agent-Updater")
	response, err := client.Do(request)
	if err != nil {
		return githubRelease{}, fmt.Errorf("GitHub release request: %w", err)
	}
	if response.StatusCode == http.StatusNotFound && allowPrereleaseFallback {
		response.Body.Close()
		listURL := strings.TrimSuffix(endpoint, "/latest") + "?per_page=20"
		listRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if requestErr != nil {
			return githubRelease{}, requestErr
		}
		listRequest.Header.Set("Accept", "application/vnd.github+json")
		listRequest.Header.Set("User-Agent", "AliCDT-Manager-Agent-Updater")
		listResponse, listErr := client.Do(listRequest)
		if listErr != nil {
			return githubRelease{}, fmt.Errorf("GitHub release list request: %w", listErr)
		}
		defer listResponse.Body.Close()
		if listResponse.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(listResponse.Body, 4096))
			return githubRelease{}, fmt.Errorf("GitHub release list returned %s: %s", listResponse.Status, strings.TrimSpace(string(body)))
		}
		var releases []githubRelease
		if decodeErr := json.NewDecoder(io.LimitReader(listResponse.Body, 2<<20)).Decode(&releases); decodeErr != nil {
			return githubRelease{}, fmt.Errorf("decode GitHub release list: %w", decodeErr)
		}
		if len(releases) == 0 {
			return githubRelease{}, errors.New("GitHub has no releases")
		}
		return releases[0], nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return githubRelease{}, fmt.Errorf("GitHub release request returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return githubRelease{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	return release, nil
}

func downloadReleaseFile(ctx context.Context, client *http.Client, rawURL string, maxSize int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "AliCDT-Manager-Agent-Updater")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, errors.New("release asset is too large")
	}
	return data, nil
}

func writeReleaseFile(dir, name string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".agent-release-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}

func releaseChecksum(path, asset string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[1] == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("agent checksum is not available")
}

func releaseChecksumBytes(data []byte, asset string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[1] == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("checksum for %s is not available", asset)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
