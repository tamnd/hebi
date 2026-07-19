package main

import (
	"fmt"
	"time"
)

func show(layout, value string) {
	t, err := time.Parse(layout, value)
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(t.Format(time.RFC3339Nano), t.Location())
}

func main() {
	show("2006-01-02", "2026-07-20")
	show("2006-01-02 15:04:05", "2026-07-20 14:30:15")
	show(time.RFC3339, "2026-07-20T14:30:15Z")
	show(time.RFC3339, "2026-01-05T09:08:07+05:30")
	show(time.RFC3339Nano, "2026-07-20T14:30:15.123456789Z")
	show("Jan 2, 2006", "Jul 20, 2026")
	show("Mon Jan _2 15:04:05 2006", "Mon Jul 20 14:30:15 2026")
	show("3:04 PM", "2:30 PM")
	show("2006-01-02T15:04:05.000", "2026-07-20T14:30:15.250")

	show("2006-01-02", "2026-13-20")
	show("2006-01-02", "2026-07-40")
	show("2006-01-02 15:04:05", "2026-07-20 25:00:00")
	show("2006-01-02", "notadate")
	show("2006-01-02", "2026-07-20 extra")

	orig := time.Date(1998, 11, 30, 6, 15, 45, 0, time.UTC)
	s := orig.Format(time.RFC3339)
	back, err := time.Parse(time.RFC3339, s)
	fmt.Println(err == nil, orig.Equal(back), s)
}
