package proxy

import (
	"strings"
	"testing"

	"github.com/alpkeskin/rota/core/internal/models"
)

func strPtr(s string) *string { return &s }

func TestPrepareProxyForRequest_BrightDataPerRequest(t *testing.T) {
	country := "DE"
	user := "brd-customer-test-zone-demo"
	p := &models.Proxy{
		Address:         "brd.superproxy.io:44445",
		Protocol:        "http",
		Username:        &user,
		TargetCountry:   &country,
		SessionStrategy: "per_request",
	}

	first, session1 := PrepareProxyForRequest(p)
	second, session2 := PrepareProxyForRequest(p)

	if session1 == "" || session2 == "" {
		t.Fatal("expected generated provider sessions")
	}
	if session1 == session2 {
		t.Fatalf("expected fresh session per request, got %q twice", session1)
	}
	if !strings.Contains(*first.Username, "-country-de") {
		t.Fatalf("username %q does not contain country-de", *first.Username)
	}
	if !strings.Contains(*first.Username, "-session-"+session1) {
		t.Fatalf("username %q does not contain generated session", *first.Username)
	}
	if *p.Username != user {
		t.Fatalf("stored proxy username was mutated: %q", *p.Username)
	}
	if first.SessionStrategy != "none" {
		t.Fatalf("effective proxy strategy = %q, want none", first.SessionStrategy)
	}
}

func TestPrepareProxyForRequest_BrightDataFixedDebugSession(t *testing.T) {
	country := "BR"
	session := "debug-br-01"
	user := "brd-customer-test-zone-demo-country-de-session-old"
	p := &models.Proxy{
		Provider:        "brightdata",
		Address:         "brd.superproxy.io:44445",
		Protocol:        "http",
		Username:        &user,
		TargetCountry:   &country,
		SessionStrategy: "fixed",
		SessionID:       &session,
	}

	got, gotSession := PrepareProxyForRequest(p)
	if gotSession != "debugbr01" {
		t.Fatalf("session = %q, want sanitized debugbr01", gotSession)
	}
	if strings.Contains(*got.Username, "-country-de") {
		t.Fatalf("old country remained in %q", *got.Username)
	}
	if !strings.Contains(*got.Username, "-country-br") {
		t.Fatalf("new country missing from %q", *got.Username)
	}
	if strings.Contains(*got.Username, "-session-old") {
		t.Fatalf("old session remained in %q", *got.Username)
	}
	if !strings.Contains(*got.Username, "-session-debugbr01") {
		t.Fatalf("fixed debug session missing from %q", *got.Username)
	}
}

func TestPrepareProxyForRequest_NonBrightDataUntouched(t *testing.T) {
	user := "ordinary-user"
	country := "CA"
	p := &models.Proxy{
		Provider:        "other",
		Address:         "proxy.example.com:8080",
		Protocol:        "http",
		Username:        &user,
		TargetCountry:   &country,
		SessionStrategy: "per_request",
	}

	got, session := PrepareProxyForRequest(p)
	if session != "" {
		t.Fatalf("unexpected provider session %q", session)
	}
	if got.Username == nil || *got.Username != user {
		t.Fatalf("username changed: %#v", got.Username)
	}
}

func TestUsesPerRequestSession_AutoDetectsBrightDataGateway(t *testing.T) {
	p := &models.Proxy{
		Address:         "brd.superproxy.io:44445",
		SessionStrategy: "per_request",
	}
	if !UsesPerRequestSession(p) {
		t.Fatal("expected Bright Data gateway to be auto-detected")
	}
}
