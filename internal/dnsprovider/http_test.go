package dnsprovider

import "testing"

func TestNewRejectsUnsafeProviderEndpoints(t *testing.T) {
	cases := []string{
		"api.example.com",
		"ftp://api.example.com",
		"https://user:password@api.example.com",
		"https://api.example.com?token=secret",
		"https://api.example.com#fragment",
	}
	for _, endpoint := range cases {
		if _, err := New(Config{Type: "cloudflare", Zone: "example.com", Endpoint: endpoint}); err == nil {
			t.Errorf("unsafe endpoint %q was accepted", endpoint)
		}
	}
	if _, err := New(Config{Type: "aliyun", Zone: "example.com", Endpoint: "http://127.0.0.1:8080/api"}); err != nil {
		t.Fatalf("valid custom endpoint was rejected: %v", err)
	}
}
