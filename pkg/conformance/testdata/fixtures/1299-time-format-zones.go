package main

import (
	"fmt"
	"time"
)

func main() {
	t := time.Date(2026, 7, 20, 14, 30, 15, 123456789, time.UTC)
	fmt.Println(t.Format("2006-01-02"))
	fmt.Println(t.Format("2006-01-02 15:04:05"))
	fmt.Println(t.Format(time.RFC3339))
	fmt.Println(t.Format(time.RFC3339Nano))
	fmt.Println(t.Format("Mon Jan _2 15:04:05 2006"))
	fmt.Println(t.Format("Monday, January 2, 2006"))
	fmt.Println(t.Format("3:04:05 PM"))
	fmt.Println(t.Format(time.Kitchen))
	fmt.Println(t.Format("2006-01-02T15:04:05.000"))
	fmt.Println(t.Format("15:04:05.999999999"))
	fmt.Println(t.Format("002"))

	tz := time.FixedZone("IST", 5*3600+30*60)
	tt := time.Date(2026, 1, 5, 9, 8, 7, 0, tz)
	fmt.Println(tt.Format(time.RFC3339))
	fmt.Println(tt.Format("2006-01-02 15:04:05 -0700 MST"))
	fmt.Println(tt.Format("Z07:00"))
	fmt.Println(tt.Format("-07"))
	fmt.Println(t.Format("Z07:00"))
}
