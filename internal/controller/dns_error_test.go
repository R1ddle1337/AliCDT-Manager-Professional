package controller

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDNSErrorMessageIsBoundedWithoutBreakingUTF8(t *testing.T) {
	message := dnsErrorMessage(errors.New(strings.Repeat("错", 600)))
	if len([]rune(message)) != 500 || !utf8.ValidString(message) {
		t.Fatalf("DNS error was not safely bounded: runes=%d valid=%t", len([]rune(message)), utf8.ValidString(message))
	}
}
