package sdk

import (
	"testing"
	"time"
)

func TestHistoryMaxDays(t *testing.T) {
	cases := []struct {
		name string
		conf interface{}
		want int
	}{
		{"nil conf", nil, 0},
		{"missing scheduling", map[string]interface{}{"sku": "x"}, 0},
		{"missing maxDays", map[string]interface{}{"scheduling": map[string]interface{}{"history": map[string]interface{}{}}}, 0},
		{"present", map[string]interface{}{"scheduling": map[string]interface{}{"history": map[string]interface{}{"maxDays": 730}}}, 730},
		{"zero", map[string]interface{}{"scheduling": map[string]interface{}{"history": map[string]interface{}{"maxDays": 0}}}, 0},
	}
	for _, c := range cases {
		if got := historyMaxDays(c.conf); got != c.want {
			t.Errorf("%s: historyMaxDays = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestGetDateRangeRetention(t *testing.T) {
	today := time.Now()
	start := today.AddDate(0, 0, -20).Format("2006-01-02")
	end := today.AddDate(0, 0, -1).Format("2006-01-02")

	confWithMaxDays := func(d int) interface{} {
		return map[string]interface{}{
			"scheduling": map[string]interface{}{
				"history": map[string]interface{}{"maxDays": d},
			},
		}
	}

	// Gated OFF : pas de maxDays → toutes les dates conservées (20 jours).
	off, err := GetDateRange(ConfigFile{
		RequestParams: RequestParams{StartDate: start, EndDate: end},
		ConnectorConf: confWithMaxDays(0),
	})
	if err != nil {
		t.Fatalf("unexpected error (off): %v", err)
	}
	if len(off) != 20 {
		t.Errorf("gated off: got %d dates, want 20", len(off))
	}

	// Gated ON : maxDays=10 → seules les dates >= today-10 restent (10 jours).
	on, err := GetDateRange(ConfigFile{
		RequestParams: RequestParams{StartDate: start, EndDate: end},
		ConnectorConf: confWithMaxDays(10),
	})
	if err != nil {
		t.Fatalf("unexpected error (on): %v", err)
	}
	if len(on) != 10 {
		t.Errorf("gated on: got %d dates, want 10", len(on))
	}
	cutoff := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location()).AddDate(0, 0, -10)
	for _, d := range on {
		if d.Before(cutoff) {
			t.Errorf("date %s is before cutoff %s, should have been skipped", d.Format("2006-01-02"), cutoff.Format("2006-01-02"))
		}
	}
}
