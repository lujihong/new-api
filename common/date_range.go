package common

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

var BeijingLocation = mustLoadLocation("Asia/Shanghai")

func mustLoadLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return location
}

// ParseRequiredUnixSecondRange parses a half-open [start,end) range.
func ParseRequiredUnixSecondRange(startValue, endValue string) (int64, int64, error) {
	if startValue == "" || endValue == "" {
		return 0, 0, errors.New("start_timestamp and end_timestamp are required")
	}
	start, err := strconv.ParseInt(startValue, 10, 64)
	if err != nil || start <= 0 {
		return 0, 0, errors.New("start_timestamp must be a positive integer second")
	}
	end, err := strconv.ParseInt(endValue, 10, 64)
	if err != nil || end <= 0 {
		return 0, 0, errors.New("end_timestamp must be a positive integer second")
	}
	if end <= start {
		return 0, 0, errors.New("end_timestamp must be greater than start_timestamp")
	}
	return start, end, nil
}

// DateRangeFromPreset returns a Beijing-time half-open range. Presets are day,
// week, month, and year. The optional history date anchors the period at that
// Beijing calendar date instead of today.
func DateRangeFromPreset(preset, history string, now time.Time) (int64, int64, error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.In(BeijingLocation)
	anchor := now
	if history != "" {
		parsed, err := time.ParseInLocation("2006-01-02", history, BeijingLocation)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid history date: %w", err)
		}
		anchor = parsed
	}
	start := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, BeijingLocation)
	switch preset {
	case "day":
	case "week":
		weekday := (int(start.Weekday()) + 6) % 7
		start = start.AddDate(0, 0, -weekday)
	case "month":
		start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, BeijingLocation)
	case "year":
		start = time.Date(start.Year(), time.January, 1, 0, 0, 0, 0, BeijingLocation)
	default:
		return 0, 0, errors.New("preset must be day, week, month, or year")
	}
	end := start.AddDate(0, 0, 1)
	switch preset {
	case "week":
		end = start.AddDate(0, 0, 7)
	case "month":
		end = start.AddDate(0, 1, 0)
	case "year":
		end = start.AddDate(1, 0, 0)
	}
	return start.Unix(), end.Unix(), nil
}

func BeijingISOTime(unixSeconds int64) string {
	return time.Unix(unixSeconds, 0).In(BeijingLocation).Format(time.RFC3339)
}
