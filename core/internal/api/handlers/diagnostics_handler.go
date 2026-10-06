package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	proxycore "github.com/alpkeskin/rota/core/internal/proxy"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

const (
	brightDataGeoURL = "https://geo.brdtest.com/mygeo.json"
	publicIPURL      = "https://api.ipify.org"
)

type DiagnosticsHandler struct {
	proxyRepo *repository.ProxyRepository
	logger    *logger.Logger
}

func NewDiagnosticsHandler(proxyRepo *repository.ProxyRepository, log *logger.Logger) *DiagnosticsHandler {
	return &DiagnosticsHandler{proxyRepo: proxyRepo, logger: log}
}

type brightDataGeoResponse struct {
	IP        string `json:"ip"`
	IPVersion int    `json:"ip_version"`
	Country   string `json:"country"`
	ASN       struct {
		ASNum   int    `json:"asnum"`
		OrgName string `json:"org_name"`
	} `json:"asn"`
}

// Exit observes the current exit identity for explicitly selected logical
// proxies. This does not affect proxy health and is safe for rotating providers.
func (h *DiagnosticsHandler) Exit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProxyIDs []int `json:"proxy_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.ProxyIDs) == 0 {
		http.Error(w, `{"error":"proxy_ids is required"}`, http.StatusBadRequest)
		return
	}

	out := make([]models.ProxyExitObservation, 0, len(req.ProxyIDs))
	for _, id := range req.ProxyIDs {
		p, err := h.proxyRepo.GetByID(r.Context(), id)
		if err != nil || p == nil {
			continue
		}

		effective, sessionID := proxycore.PrepareProxyForRequest(p)
		transport, err := proxycore.CreateProxyTransport(effective)
		if err != nil {
			h.logger.Warn("exit observation transport failed", "proxy_id", id, "error", err)
			continue
		}
		if proxycore.UsesPerRequestSession(p) {
			transport.DisableKeepAlives = true
		}
		client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
		obs, err := h.observeExitWithClient(r.Context(), client, p, sessionID)
		transport.CloseIdleConnections()
		if err != nil {
			h.logger.Warn("exit observation failed", "proxy_id", id, "error", err)
			continue
		}

		h.persistExit(r.Context(), obs)
		out = append(out, obs)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"exits": out}) //nolint:errcheck
}

// Probe explicitly performs target HTTP requests from Rota. Unlike normal HTTPS
// proxy traffic this endpoint is the TLS client, so it can observe the target
// response status (200/403/etc.) without MITM.
//
// With session_strategy=per_request every URL+attempt gets a fresh provider
// session. With session_strategy=fixed the configured session id is reused,
// which is useful for debugging direct-vs-Rota behavior on the same peer.
func (h *DiagnosticsHandler) Probe(w http.ResponseWriter, r *http.Request) {
	var req models.DiagnosticProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if len(req.URLs) == 0 || len(req.ProxyIDs) == 0 {
		http.Error(w, `{"error":"urls and proxy_ids are required"}`, http.StatusBadRequest)
		return
	}
	if len(req.URLs) > 500 || len(req.ProxyIDs) > 100 {
		http.Error(w, `{"error":"too many urls or proxies"}`, http.StatusBadRequest)
		return
	}
	if req.Attempts <= 0 {
		req.Attempts = 1
	}
	if req.Attempts > 20 {
		http.Error(w, `{"error":"attempts must be <= 20"}`, http.StatusBadRequest)
		return
	}

	results := make([]models.DiagnosticProbeResult, 0, len(req.URLs)*len(req.ProxyIDs)*req.Attempts)

	for _, proxyID := range req.ProxyIDs {
		p, err := h.proxyRepo.GetByID(r.Context(), proxyID)
		if err != nil || p == nil {
			continue
		}

		// URL is deliberately outside attempts so each URL can be sampled N times
		// while per_request still materializes a fresh session every time.
		for _, targetURL := range req.URLs {
			for attempt := 0; attempt < req.Attempts; attempt++ {
				result := h.probeOnce(r.Context(), p, targetURL, req.FollowRedirects)
				results = append(results, result)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"results": results}) //nolint:errcheck
}

func (h *DiagnosticsHandler) probeOnce(
	ctx context.Context,
	p *models.Proxy,
	targetURL string,
	followRedirects bool,
) models.DiagnosticProbeResult {
	testedAt := time.Now().UTC()
	effective, sessionID := proxycore.PrepareProxyForRequest(p)
	result := models.DiagnosticProbeResult{
		URL:       targetURL,
		ProxyID:   p.ID,
		ProxyName: p.Name,
		Provider:  p.Provider,
		SessionID: sessionID,
		TestedAt:  testedAt,
	}
	if p.TargetCountry != nil {
		result.ConfiguredCountry = strings.ToUpper(*p.TargetCountry)
	}

	transport, err := proxycore.CreateProxyTransport(effective)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer transport.CloseIdleConnections()
	if proxycore.UsesPerRequestSession(p) {
		// Do not let Go reuse an upstream TCP connection across logical requests
		// when the selected provider session is explicitly "fresh per request".
		transport.DisableKeepAlives = true
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   45 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if !followRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}

	// Use the same prepared provider session for exit observation and target
	// request so the reported geo belongs to the peer used for this probe.
	if proxycore.IsBrightData(p) {
		if obs, err := h.observeExitWithClient(ctx, client, p, sessionID); err == nil {
			result.ExitIP = obs.ExitIP
			result.ExitCountry = obs.ExitCountry
			result.ExitASN = obs.ExitASN
			h.persistExit(ctx, obs)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Rota-Diagnostics/1.0)")

	started := time.Now()
	resp, err := client.Do(httpReq)
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	result.HTTPStatus = resp.StatusCode
	result.FinalURL = resp.Request.URL.String()
	return result
}

func (h *DiagnosticsHandler) observeExitWithClient(
	ctx context.Context,
	client *http.Client,
	p *models.Proxy,
	sessionID string,
) (models.ProxyExitObservation, error) {
	geo, err := fetchBrightDataGeo(ctx, client)
	if err != nil {
		return models.ProxyExitObservation{}, err
	}

	exitIP := strings.TrimSpace(geo.IP)
	if exitIP == "" {
		// geo.brdtest.com currently does not always include the IP in its JSON;
		// query ipify through the same provider session to capture it explicitly.
		if ip, ipErr := fetchPublicIP(ctx, client); ipErr == nil {
			exitIP = ip
		}
	}

	obs := models.ProxyExitObservation{
		ProxyID:          p.ID,
		ProxyName:        p.Name,
		ExitIP:           exitIP,
		ExitCountry:      strings.ToUpper(geo.Country),
		ExitASN:          geo.ASN.ASNum,
		ExitOrganization: geo.ASN.OrgName,
		SessionID:        sessionID,
		ObservedAt:       time.Now().UTC(),
	}
	if p.TargetCountry != nil {
		obs.ConfiguredCountry = strings.ToUpper(*p.TargetCountry)
		obs.CountryMatches = strings.EqualFold(obs.ConfiguredCountry, obs.ExitCountry)
	}
	return obs, nil
}

func fetchBrightDataGeo(ctx context.Context, client *http.Client) (brightDataGeoResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, brightDataGeoURL, nil)
	if err != nil {
		return brightDataGeoResponse{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return brightDataGeoResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return brightDataGeoResponse{}, fmt.Errorf("geo endpoint HTTP %d", resp.StatusCode)
	}
	var geo brightDataGeoResponse
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil {
		return brightDataGeoResponse{}, err
	}
	return geo, nil
}

func fetchPublicIP(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicIPURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ip endpoint HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 128)
	n, _ := resp.Body.Read(buf)
	return strings.TrimSpace(string(buf[:n])), nil
}

func (h *DiagnosticsHandler) persistExit(ctx context.Context, obs models.ProxyExitObservation) {
	_, err := h.proxyRepo.GetDB().Pool.Exec(ctx, `
		UPDATE proxies SET
			last_exit_ip=$1,
			last_exit_country=$2,
			last_exit_asn=$3,
			last_exit_org=$4,
			last_exit_observed_at=$5,
			updated_at=NOW()
		WHERE id=$6
	`, obs.ExitIP, obs.ExitCountry, obs.ExitASN, obs.ExitOrganization, obs.ObservedAt, obs.ProxyID)
	if err != nil {
		h.logger.Warn("failed to persist exit observation", "proxy_id", obs.ProxyID, "error", err)
	}
}
