package main

import (
	"fmt"
	"time"
)

type Event struct {
	Name string
	At   time.Time
}

func main() {
	t := time.Date(2026, 7, 20, 14, 30, 15, 250000000, time.UTC)
	fmt.Println(t)
	fmt.Println(t.Year(), t.Month(), t.Day(), t.Weekday(), t.YearDay())
	fmt.Println(t.Hour(), t.Minute(), t.Second(), t.Nanosecond())
	fmt.Println(t.Unix(), t.UnixNano(), t.UnixMilli(), t.UnixMicro())

	y, m, d := t.Date()
	h, mi, s := t.Clock()
	fmt.Println(y, m, d, h, mi, s)

	later := t.Add(36 * time.Hour)
	fmt.Println(later)
	fmt.Println(later.Sub(t))
	fmt.Println(t.AddDate(0, 1, 10))
	fmt.Println(t.Before(later), t.After(later), t.Equal(t))

	var zero time.Time
	fmt.Println(zero, zero.IsZero(), t.IsZero())

	fmt.Println(time.Date(2026, 14, 40, 26, 70, 70, 0, time.UTC))

	e := Event{Name: "launch", At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	fmt.Println(e.Name, e.At)
	fmt.Println(time.January, time.December, time.Monday, time.Sunday)
}
