package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
)

type UsageRepository struct {
	db *database.DB
}

func NewUsageRepository(db *database.DB) *UsageRepository {
	return &UsageRepository{db: db}
}

type UsageFilter struct {
	ProxyUserID *int
	Project     string
	From        *time.Time
	To          *time.Time
}

func (r *UsageRepository) Export(ctx context.Context, f UsageFilter) ([]models.UsageExportRow, models.UsageTotals, error) {
	where := []string{"1=1"}
	args := []any{}
	n := 1

	if f.ProxyUserID != nil {
		where = append(where, fmt.Sprintf("pr.proxy_user_id = $%d", n))
		args = append(args, *f.ProxyUserID)
		n++
	}
	if strings.TrimSpace(f.Project) != "" {
		where = append(where, fmt.Sprintf("pu.username = $%d", n))
		args = append(args, strings.TrimSpace(f.Project))
		n++
	}
	if f.From != nil {
		where = append(where, fmt.Sprintf("pr.timestamp >= $%d", n))
		args = append(args, *f.From)
		n++
	}
	if f.To != nil {
		where = append(where, fmt.Sprintf("pr.timestamp < $%d", n))
		args = append(args, *f.To)
		n++
	}

	q := fmt.Sprintf(`
		SELECT
			time_bucket('1 day', pr.timestamp) AS day,
			pr.proxy_user_id,
			COALESCE(pu.username, 'legacy') AS project,
			pr.pool_id,
			COALESCE(pp.name, '') AS pool_name,
			pr.proxy_id,
			COALESCE(p.name, '') AS proxy_name,
			COALESCE(NULLIF(pr.proxy_address,''), p.address, '') AS proxy_address,
			COALESCE(p.provider, '') AS provider,
			COALESCE(p.target_country, '') AS target_country,
			COUNT(*)::bigint AS requests,
			COALESCE(SUM(pr.bytes_up),0)::bigint AS bytes_up,
			COALESCE(SUM(pr.bytes_down),0)::bigint AS bytes_down
		FROM proxy_requests pr
		LEFT JOIN proxy_users pu ON pu.id = pr.proxy_user_id
		LEFT JOIN proxy_pools pp ON pp.id = pr.pool_id
		LEFT JOIN proxies p ON p.id = pr.proxy_id
		WHERE %s
		GROUP BY day, pr.proxy_user_id, pu.username, pr.pool_id, pp.name,
		         pr.proxy_id, p.name, pr.proxy_address, p.address, p.provider, p.target_country
		ORDER BY day DESC, project, pool_name, proxy_name, proxy_address
	`, strings.Join(where, " AND "))

	rows, err := r.db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, models.UsageTotals{}, fmt.Errorf("query usage export: %w", err)
	}
	defer rows.Close()

	out := []models.UsageExportRow{}
	var totals models.UsageTotals
	for rows.Next() {
		var row models.UsageExportRow
		if err := rows.Scan(
			&row.Day, &row.ProxyUserID, &row.Project, &row.PoolID, &row.PoolName,
			&row.ProxyID, &row.ProxyName, &row.ProxyAddress, &row.Provider,
			&row.TargetCountry, &row.Requests, &row.BytesUp, &row.BytesDown,
		); err != nil {
			return nil, models.UsageTotals{}, err
		}
		row.TotalBytes = row.BytesUp + row.BytesDown
		totals.Requests += row.Requests
		totals.BytesUp += row.BytesUp
		totals.BytesDown += row.BytesDown
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, models.UsageTotals{}, err
	}
	totals.TotalBytes = totals.BytesUp + totals.BytesDown
	return out, totals, nil
}
