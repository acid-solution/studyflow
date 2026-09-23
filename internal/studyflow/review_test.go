package studyflow

import (
	"testing"
	"time"
)

func TestBeginningOfWeek(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	wednesday := time.Date(2026, 9, 23, 18, 30, 0, 0, location)
	if got := beginningOfWeek(wednesday, "monday").Format("2006-01-02"); got != "2026-09-21" {
		t.Fatalf("monday start = %s", got)
	}
	if got := beginningOfWeek(wednesday, "sunday").Format("2006-01-02"); got != "2026-09-20" {
		t.Fatalf("sunday start = %s", got)
	}
}

func TestDateOnlyKeepsLocation(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	value := dateOnly(time.Date(2026, 9, 23, 23, 59, 0, 0, location))
	if value.Location() != location || value.Hour() != 0 || value.Day() != 23 {
		t.Fatalf("unexpected date: %v", value)
	}
}
