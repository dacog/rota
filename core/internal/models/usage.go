package models

import "time"

// UsageExportRow is a project-oriented usage aggregation. Proxy users are used
// as the project identity for distinct consumers/projects.
type UsageExportRow struct {
	Day            time.Time `json:"day"`
	ProxyUserID    *int      `json:"proxy_user_id,omitempty"`
	Project        string    `json:"project"`
	PoolID         *int      `json:"pool_id,omitempty"`
	PoolName       string    `json:"pool_name,omitempty"`
	ProxyID        int       `json:"proxy_id"`
	ProxyName      string    `json:"proxy_name,omitempty"`
	ProxyAddress   string    `json:"proxy_address"`
	Provider       string    `json:"provider,omitempty"`
	TargetCountry  string    `json:"target_country,omitempty"`
	Requests       int64     `json:"requests"`
	BytesUp        int64     `json:"bytes_up"`
	BytesDown      int64     `json:"bytes_down"`
	TotalBytes     int64     `json:"total_bytes"`
}

// UsageTotals is returned alongside JSON exports for a quick project summary.
type UsageTotals struct {
	Requests   int64 `json:"requests"`
	BytesUp    int64 `json:"bytes_up"`
	BytesDown  int64 `json:"bytes_down"`
	TotalBytes int64 `json:"total_bytes"`
}
