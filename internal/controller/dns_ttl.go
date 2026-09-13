package controller

import (
	"errors"
	"strings"
)

// Cloudflare uses the value 1 to request its provider-managed automatic TTL.
// Other providers use 60 seconds as the shortest portable value, so the
// controller translates that sentinel before persisting records for them.
const (
	cloudflareAutomaticTTL = 1
	dnsTTLMinimumSeconds   = 30
	dnsTTLFastestSeconds   = 60
	dnsTTLDefaultSeconds   = dnsTTLFastestSeconds
	dnsTTLMaximumSeconds   = 86400
)

func normalizeDNSProviderTTL(ttl int, providerType string) (int, error) {
	if ttl == cloudflareAutomaticTTL {
		if strings.EqualFold(strings.TrimSpace(providerType), "cloudflare") {
			return cloudflareAutomaticTTL, nil
		}
		return dnsTTLDefaultSeconds, nil
	}
	if ttl < dnsTTLMinimumSeconds {
		return dnsTTLDefaultSeconds, nil
	}
	if strings.EqualFold(strings.TrimSpace(providerType), "cloudflare") && ttl < dnsTTLFastestSeconds {
		return dnsTTLFastestSeconds, nil
	}
	if ttl > dnsTTLMaximumSeconds {
		return 0, errors.New("DNS TTL must not exceed 86400 seconds")
	}
	return ttl, nil
}
