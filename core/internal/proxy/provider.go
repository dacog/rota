package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
)

var (
	brightDataCountryRE = regexp.MustCompile(`-country-[A-Za-z]{2}`)
	brightDataSessionRE = regexp.MustCompile(`-session-[A-Za-z0-9_.-]+`)
)

// UsesPerRequestSession reports whether each outgoing upstream request/CONNECT
// should receive a fresh provider session. This is primarily useful for
// providers such as Bright Data where a session id selects a sticky exit peer.
func IsBrightData(p *models.Proxy) bool {
	if p == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(p.Provider), "brightdata") {
		return true
	}
	host := strings.ToLower(strings.TrimSpace(p.Address))
	return strings.Contains(host, "brd.superproxy.io") || strings.Contains(host, "brightdata")
}

func UsesPerRequestSession(p *models.Proxy) bool {
	return IsBrightData(p) &&
		strings.EqualFold(strings.TrimSpace(p.SessionStrategy), "per_request")
}

// PrepareProxyForRequest returns a shallow copy of p with provider-specific
// routing parameters materialized in the username. The stored proxy row is
// never mutated.
//
// Bright Data behavior:
//   - target_country becomes -country-xx
//   - session_strategy=per_request creates a fresh -session-... token
//   - session_strategy=fixed uses session_id for deterministic debugging
//
// The returned proxy has session_strategy=none so callers can safely pass the
// prepared value into CreateProxyTransport without generating a second session.
func PrepareProxyForRequest(p *models.Proxy) (*models.Proxy, string) {
	if p == nil {
		return nil, ""
	}

	cp := *p
	if p.Username != nil {
		u := *p.Username
		cp.Username = &u
	}
	if p.Password != nil {
		pw := *p.Password
		cp.Password = &pw
	}

	if !IsBrightData(p) || cp.Username == nil || *cp.Username == "" {
		return &cp, ""
	}

	username := *cp.Username
	strategy := strings.ToLower(strings.TrimSpace(p.SessionStrategy))
	if strategy == "per_request" || strategy == "fixed" {
		// Remove an existing session before adding country/session components.
		// The session regex intentionally allows '-' and would otherwise consume
		// a country component appended after the old session.
		username = brightDataSessionRE.ReplaceAllString(username, "")
	}

	if p.TargetCountry != nil {
		country := strings.ToLower(strings.TrimSpace(*p.TargetCountry))
		if len(country) == 2 {
			username = brightDataCountryRE.ReplaceAllString(username, "")
			username += "-country-" + country
		}
	}

	sessionID := ""
	switch strategy {
	case "per_request":
		sessionID = newProviderSessionID()
	case "fixed":
		if p.SessionID != nil {
			sessionID = sanitizeSessionID(*p.SessionID)
		}
	}

	if sessionID != "" {
		username += "-session-" + sessionID
	}

	cp.Username = &username
	cp.SessionStrategy = "none"
	cp.SessionID = nil
	return &cp, sessionID
}

func sanitizeSessionID(v string) string {
	v = strings.TrimSpace(v)
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_',
			r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func newProviderSessionID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return "rota" + hex.EncodeToString(buf)
	}
	return "rota" + strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "")
}
