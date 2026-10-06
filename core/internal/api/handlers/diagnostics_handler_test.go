package handlers

import (
	"testing"

	"github.com/alpkeskin/rota/core/internal/models"
)

func TestSummarizeDiagnostics(t *testing.T) {
	results := []models.DiagnosticProbeResult{
		{URL: "https://a.example", ConfiguredCountry: "DE", HTTPStatus: 403},
		{URL: "https://a.example", ConfiguredCountry: "DE", HTTPStatus: 403},
		{URL: "https://a.example", ConfiguredCountry: "BR", HTTPStatus: 200},
		{URL: "https://a.example", ConfiguredCountry: "BR", HTTPStatus: 403},
		{URL: "https://b.example", ConfiguredCountry: "CA", Error: "timeout"},
	}

	got := summarizeDiagnostics(results)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}

	assert := func(url, country, assessment string, reachable, forbidden, errors int) {
		t.Helper()
		for _, row := range got {
			if row.URL == url && row.Country == country {
				if row.Assessment != assessment ||
					row.Reachable != reachable ||
					row.Forbidden403 != forbidden ||
					row.Errors != errors {
					t.Fatalf("row %+v does not match expected assessment=%s reachable=%d forbidden=%d errors=%d",
						row, assessment, reachable, forbidden, errors)
				}
				return
			}
		}
		t.Fatalf("missing row %s %s", url, country)
	}

	assert("https://a.example", "DE", "blocked_sample", 0, 2, 0)
	assert("https://a.example", "BR", "reachable", 1, 1, 0)
	assert("https://b.example", "CA", "inconclusive", 0, 0, 1)
}
