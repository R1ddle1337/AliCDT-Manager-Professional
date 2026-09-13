package dnsprovider

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxProviderResponseBytes = 4 << 20
	maxProviderListPages     = 100
	maxProviderErrorRunes    = 500
)

func normalizeProviderEndpoint(raw, fallback string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = fallback
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("DNS provider endpoint must include a scheme and host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported DNS provider endpoint scheme %q", parsed.Scheme)
	}
	if parsed.User != nil {
		return "", errors.New("DNS provider endpoint must not contain user credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("DNS provider endpoint must not contain a query or fragment")
	}
	parsed.RawPath = ""
	return parsed.String(), nil
}

func providerHTTPClient(existing *http.Client, endpoint string) *http.Client {
	client := &http.Client{Timeout: 15 * time.Second}
	if existing != nil {
		clone := *existing
		client = &clone
		if client.Timeout <= 0 {
			client.Timeout = 15 * time.Second
		}
	}
	parsed, _ := url.Parse(endpoint)
	origin := ""
	if parsed != nil {
		origin = parsed.Scheme + "://" + parsed.Host
	}
	originalRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("DNS provider redirected too many times")
		}
		if origin == "" || !strings.EqualFold(request.URL.Scheme+"://"+request.URL.Host, origin) {
			return errors.New("DNS provider redirect changed origin")
		}
		if originalRedirect != nil {
			return originalRedirect(request, via)
		}
		return nil
	}
	return client
}

func readProviderResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxProviderResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxProviderResponseBytes {
		return nil, errors.New("DNS provider response is too large")
	}
	return data, nil
}

func boundedProviderMessage(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxProviderErrorRunes {
		value = string(runes[:maxProviderErrorRunes])
	}
	return value
}
