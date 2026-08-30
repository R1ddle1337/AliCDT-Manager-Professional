package controller

import (
	"bufio"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
)

func (s *Server) agentRelease(w http.ResponseWriter, r *http.Request) {
	arch := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("arch")))
	if arch == "" {
		arch = runtime.GOARCH
	}
	if arch != "amd64" && arch != "arm64" {
		writeError(w, http.StatusBadRequest, errors.New("unsupported agent architecture"))
		return
	}
	asset := "cdt-relay-agent-linux-" + arch
	if s.agentAssetsDir == "" {
		http.NotFound(w, r)
		return
	}
	assetPath := filepath.Join(s.agentAssetsDir, asset)
	info, err := os.Stat(assetPath)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	checksum, err := releaseChecksum(filepath.Join(s.agentAssetsDir, "checksums.txt"), asset)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	current := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sha256")))
	writeJSON(w, http.StatusOK, protocol.AgentRelease{Available: current == "" || current != checksum, Version: s.agentVersion, Architecture: arch, SHA256: checksum, URL: "/agent/" + asset, Size: info.Size()})
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
