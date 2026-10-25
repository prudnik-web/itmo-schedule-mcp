package itmo

import (
	"time"
	_ "time/tzdata"
)

const MaxRangeDays = 31

var Moscow = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		panic("Moscow timezone unavailable")
	}
	return loc
}()

type DateRange struct {
	Start string
	End   string
}

func Dates(start, end string, now time.Time) (DateRange, error) {
	if start == "" {
		start = now.In(Moscow).Format(time.DateOnly)
	}
	s, err := time.Parse(time.DateOnly, start)
	if err != nil || s.Year() < 2000 || s.Year() > 2100 {
		return DateRange{}, InvalidInput
	}
	if end == "" {
		end = s.AddDate(0, 0, 6).Format(time.DateOnly)
	}
	e, err := time.Parse(time.DateOnly, end)
	if err != nil || e.Before(s) || e.Sub(s) >= MaxRangeDays*24*time.Hour || e.Year() > 2100 {
		return DateRange{}, InvalidInput
	}
	return DateRange{Start: start, End: end}, nil
}
