package models

import "time"

// ProxyExitObservation is the last exit identity observed by an explicit exit
// probe. For rotating providers this describes that probe only; it must not be
// interpreted as the exit used by every later request.
type ProxyExitObservation struct {
	ProxyID           int       `json:"proxy_id"`
	ProxyName         string    `json:"proxy_name,omitempty"`
	ConfiguredCountry string    `json:"configured_country,omitempty"`
	ExitIP            string    `json:"exit_ip"`
	ExitCountry       string    `json:"exit_country"`
	ExitASN           int       `json:"exit_asn,omitempty"`
	ExitOrganization  string    `json:"exit_organization,omitempty"`
	SessionID         string    `json:"session_id,omitempty"`
	ObservedAt        time.Time `json:"observed_at"`
	CountryMatches    bool      `json:"country_matches"`
}

// DiagnosticProbeRequest runs target requests from explicitly selected logical
// proxies. attempts>1 is useful with per_request session rotation to sample
// multiple exits from the same requested country.
type DiagnosticProbeRequest struct {
	URLs            []string `json:"urls"`
	ProxyIDs        []int    `json:"proxy_ids"`
	Attempts        int      `json:"attempts"`
	FollowRedirects bool     `json:"follow_redirects"`
}

// DiagnosticProbeResult is intentionally separate from proxy health. A target
// HTTP 403 is a target/access result and does not mark the proxy unhealthy.
type DiagnosticProbeResult struct {
	URL               string    `json:"url"`
	ProxyID           int       `json:"proxy_id"`
	ProxyName         string    `json:"proxy_name,omitempty"`
	Provider          string    `json:"provider,omitempty"`
	ConfiguredCountry string    `json:"configured_country,omitempty"`
	SessionID         string    `json:"session_id,omitempty"`
	ExitIP            string    `json:"exit_ip,omitempty"`
	ExitCountry       string    `json:"exit_country,omitempty"`
	ExitASN           int       `json:"exit_asn,omitempty"`
	HTTPStatus        int       `json:"http_status,omitempty"`
	FinalURL          string    `json:"final_url,omitempty"`
	DurationMS        int64     `json:"duration_ms"`
	Error             string    `json:"error,omitempty"`
	TestedAt          time.Time `json:"tested_at"`
}
