package skill

import (
	"strings"
	"testing"
	"time"
)

func TestResolveDate(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	got, err := resolveDate("今天")
	if err != nil {
		t.Fatal(err)
	}
	if got != today {
		t.Fatalf("today = %q, want %q", got, today)
	}

	got, err = resolveDate("2026-06-05")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-06-05" {
		t.Fatalf("date = %q", got)
	}

	if _, err := resolveDate("next friday"); err == nil {
		t.Fatal("expected invalid date error")
	}
}

func TestMockForecastIsDeterministic(t *testing.T) {
	a := mockForecast("Shanghai", "today")
	b := mockForecast("Shanghai", "today")
	if a != b {
		t.Fatalf("mock forecast not deterministic:\n%s\n%s", a, b)
	}
	if !strings.Contains(a, "Shanghai") {
		t.Fatalf("forecast = %q", a)
	}
}
