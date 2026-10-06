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

const brightDataGeoURL = "https://geo.brdtest.com/mygeo.json"

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
		geo, err := h.observeExit(r.Context(), effective)
		if err != nil {
			h.logger.Warn("exit observation failed", "proxy_id", id, "error", err)
			continue
		}
		obs := models.ProxyExitObservation{
			ProxyID:          id,
			ProxyName:        p.Name,
			ExitIP:           geoIPString(geo),
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
		h.persistExit(r.Context(), obs)
		out = append(out, obs)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"exits": out}) //nolint:errcheck
}

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
		for attempt := 0; attempt < req.Attempts; attempt++ {
			// One prepared session per attempt: geo observation and all target
			// URLs in this attempt use the same provider session/exit.
			effective, sessionID := proxycore.PrepareProxyForRequest(p)
			transport, err := proxycore.CreateProxyTransport(effective)
			if err != nil {
				continue
			}
			client := &http.Client{
				Transport: transport,
				Timeout:   45 * time.Second,
				CheckRedirect: func(_ *http.Request, via []*http.Request) error {
					if !req.FollowRedirects {
						return http.ErrUseLastResponse
					}
					if len(via) >= 10 {
						return fmt.Errorf("stopped after 10 redirects")
					}
					return nil
				},
			}

			var geo brightDataGeoResponse
			if strings.EqualFold(p.Provider, "brightdata") {
				if g, err := observeExitWithClient(r.Context(), client); err == nil {
					geo = g
					obs := models.ProxyExitObservation{
						ProxyID:          p.ID,
						ProxyName:        p.Name,
						ExitIP:           geoIPString(g),
						ExitCountry:      strings.ToUpper(g.Country),
						ExitASN:          g.ASN.ASNum,
						ExitOrganization: g.ASN.OrgName,
						SessionID:        sessionID,
						ObservedAt:       time.Now().UTC(),
					}
					if p.TargetCountry != nil {
						obs.ConfiguredCountry = strings.ToUpper(*p.TargetCountry)
						obs.CountryMatches = strings.EqualFold(obs.ConfiguredCountry, obs.ExitCountry)
					}
					h.persistExit(r.Context(), obs)
				}
			}

			for _, targetURL := range req.URLs {
				testedAt := time.Now().UTC()
				started := time.Now()
				result := models.DiagnosticProbeResult{
					URL:         targetURL,
					ProxyID:     p.ID,
					ProxyName:   p.Name,
					Provider:    p.Provider,
					SessionID:   sessionID,
					ExitIP:      geoIPString(geo),
					ExitCountry: strings.ToUpper(geo.Country),
					ExitASN:     geo.ASN.ASNum,
					TestedAt:    testedAt,
				}
				if p.TargetCountry != nil {
					result.ConfiguredCountry = strings.ToUpper(*p.TargetCountry)
				}

				httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
				if err != nil {
					result.Error = err.Error()
					results = append(results, result)
					continue
				}
				httpReq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Rota-Diagnostics/1.0)")
				resp, err := client.Do(httpReq)
				result.DurationMS = time.Since(started).Milliseconds()
				if err != nil {
					result.Error = err.Error()
				} else {
					result.HTTPStatus = resp.StatusCode
					result.FinalURL = resp.Request.URL.String()
					resp.Body.Close()
				}
				results = append(results, result)
			}
			transport.CloseIdleConnections()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"results": results}) //nolint:errcheck
}

func (h *DiagnosticsHandler) observeExit(ctx context.Context, p *models.Proxy) (brightDataGeoResponse, error) {
	transport, err := proxycore.CreateProxyTransport(p)
	if err != nil {
		return brightDataGeoResponse{}, err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	return observeExitWithClient(ctx, client)
}

func observeExitWithClient(ctx context.Context, client *http.Client) (brightDataGeoResponse, error) {
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

func geoIPString(geo brightDataGeoResponse) string { return geo.IP }

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
