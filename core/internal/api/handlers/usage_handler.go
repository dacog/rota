package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

type UsageHandler struct {
	repo   *repository.UsageRepository
	logger *logger.Logger
}

func NewUsageHandler(repo *repository.UsageRepository, log *logger.Logger) *UsageHandler {
	return &UsageHandler{repo: repo, logger: log}
}

func (h *UsageHandler) Export(w http.ResponseWriter, r *http.Request) {
	var filter repository.UsageFilter
	q := r.URL.Query()

	if v := q.Get("user_id"); v != "" {
		id, err := strconv.Atoi(v)
		if err != nil || id <= 0 {
			http.Error(w, `{"error":"invalid user_id"}`, http.StatusBadRequest)
			return
		}
		filter.ProxyUserID = &id
	}
	filter.Project = q.Get("project")
	if filter.Project == "" {
		filter.Project = q.Get("user")
	}

	parseTime := func(key string) (*time.Time, error) {
		v := q.Get(key)
		if v == "" {
			return nil, nil
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02"} {
			if t, err := time.Parse(layout, v); err == nil {
				if layout == "2006-01-02" && key == "to" {
					t = t.Add(24 * time.Hour)
				}
				return &t, nil
			}
		}
		return nil, fmt.Errorf("invalid %s", key)
	}

	var err error
	if filter.From, err = parseTime("from"); err != nil {
		http.Error(w, `{"error":"invalid from; use YYYY-MM-DD or RFC3339"}`, http.StatusBadRequest)
		return
	}
	if filter.To, err = parseTime("to"); err != nil {
		http.Error(w, `{"error":"invalid to; use YYYY-MM-DD or RFC3339"}`, http.StatusBadRequest)
		return
	}

	rows, totals, err := h.repo.Export(r.Context(), filter)
	if err != nil {
		h.logger.Error("usage export failed", "error", err)
		http.Error(w, `{"error":"usage export failed"}`, http.StatusInternalServerError)
		return
	}

	if q.Get("format") != "csv" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"usage": rows, "totals": totals}) //nolint:errcheck
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="usage.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{
		"day", "project", "proxy_user_id", "pool_id", "pool", "proxy_id", "proxy",
		"proxy_address", "provider", "target_country", "requests", "bytes_up",
		"bytes_down", "total_bytes",
	})
	for _, row := range rows {
		userID := ""
		if row.ProxyUserID != nil {
			userID = strconv.Itoa(*row.ProxyUserID)
		}
		poolID := ""
		if row.PoolID != nil {
			poolID = strconv.Itoa(*row.PoolID)
		}
		_ = cw.Write([]string{
			row.Day.Format("2006-01-02"), row.Project, userID, poolID, row.PoolName,
			strconv.Itoa(row.ProxyID), row.ProxyName, row.ProxyAddress, row.Provider,
			row.TargetCountry, strconv.FormatInt(row.Requests,10),
			strconv.FormatInt(row.BytesUp,10), strconv.FormatInt(row.BytesDown,10),
			strconv.FormatInt(row.TotalBytes,10),
		})
	}
}
