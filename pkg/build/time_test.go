package build

import (
	"testing"
)

// TestTime checks time.Time against go run. A Time is a foreign struct the runtime
// models as a shim class holding a Unix second, a nanosecond, and a Location, so
// construction normalizes out-of-range fields, the accessors read back the wall
// clock, the arithmetic returns new instants and Durations, and String and Format
// render the way Go's fmt and reference-layout formatter do across zones.
func TestTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"construction and accessors",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	t := time.Date(2026, 7, 20, 14, 30, 15, 500000000, time.UTC)
	fmt.Println(t)
	fmt.Println(t.Year(), t.Month(), t.Day())
	fmt.Println(t.Hour(), t.Minute(), t.Second(), t.Nanosecond())
	fmt.Println(t.Weekday(), t.YearDay())
	y, mo, d := t.Date()
	fmt.Println(y, mo, d)
	h, mi, s := t.Clock()
	fmt.Println(h, mi, s)
	fmt.Println(t.Unix(), t.UnixNano(), t.UnixMilli(), t.UnixMicro())
}
`,
		},
		{
			"out of range fields normalize",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println(time.Date(2026, 13, 32, 25, 61, 61, 0, time.UTC))
	fmt.Println(time.Date(2026, 0, 0, 0, 0, 0, 0, time.UTC))
	fmt.Println(time.Date(2020, 2, 29, 0, 0, 0, 0, time.UTC))
}
`,
		},
		{
			"arithmetic and comparison",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	t := time.Date(2026, 7, 20, 14, 30, 15, 0, time.UTC)
	t2 := t.Add(90 * time.Minute)
	fmt.Println(t2)
	fmt.Println(t2.Sub(t))
	fmt.Println(t.AddDate(1, 2, 3))
	fmt.Println(t.Before(t2), t.After(t2), t.Equal(t))
}
`,
		},
		{
			"zero value",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	var zero time.Time
	fmt.Println(zero, zero.IsZero())
	fmt.Println(time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).IsZero())
	fmt.Println(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).IsZero())
}
`,
		},
		{
			"month and weekday constants",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println(time.January, time.June, time.December)
	fmt.Println(time.Sunday, time.Wednesday, time.Saturday)
}
`,
		},
		{
			"fixed zone",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	tokyo := time.FixedZone("JST", 9*60*60)
	ft := time.Date(2026, 7, 20, 14, 0, 0, 0, tokyo)
	fmt.Println(ft)
	fmt.Println(ft.UTC())
	fmt.Println(ft.Unix())
	fmt.Println(ft.Location())
}
`,
		},
		{
			"format reference layouts",
			`package main

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
	fmt.Println(t.Format("Mon Jan 2 15:04:05 MST 2006"))
	fmt.Println(t.Format("Monday, January 2, 2006"))
	fmt.Println(t.Format("3:04:05 PM"))
	fmt.Println(t.Format("03:04:05 pm"))
	fmt.Println(t.Format("2006-01-02T15:04:05.000"))
	fmt.Println(t.Format("15:04:05.999999999"))
	fmt.Println(t.Format(time.Kitchen))
	fmt.Println(t.Format(time.ANSIC))
	fmt.Println(t.Format("_2 Jan 06"))
	fmt.Println(t.Format("002"))
}
`,
		},
		{
			"format across zones",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	tz := time.FixedZone("JST", 9*3600+30*60)
	tt := time.Date(2026, 1, 5, 9, 8, 7, 0, tz)
	fmt.Println(tt.Format(time.RFC3339))
	fmt.Println(tt.Format("2006-01-02 15:04:05 -0700 MST"))
	fmt.Println(tt.Format("Z07:00"))
	fmt.Println(tt.Format("-07"))
	fmt.Println(tt.Format("-070000"))
	u := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	fmt.Println(u.Format("Z07:00"))
	fmt.Println(u.Format("-0700"))
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestTimeParse checks time.Parse against go run. Parse reads a value against a
// reference-time layout and returns the instant it names, reading through UTC when
// the layout carries no zone and landing exactly on a numeric or Z zone. A bad value
// returns the zero Time and a ParseError whose text matches Go's, and Format then
// Parse round-trips.
func TestTimeParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"reference layouts read back",
			`package main

import (
	"fmt"
	"time"
)

func show(layout, value string) {
	t, err := time.Parse(layout, value)
	if err != nil {
		fmt.Println("ERR", err)
		return
	}
	fmt.Println(t.Format(time.RFC3339Nano), "|", t.Location())
}

func main() {
	show("2006-01-02", "2026-07-20")
	show("2006-01-02 15:04:05", "2026-07-20 14:30:15")
	show(time.RFC3339, "2026-07-20T14:30:15Z")
	show(time.RFC3339, "2026-07-20T14:30:15+09:30")
	show(time.RFC3339Nano, "2026-07-20T14:30:15.123456789Z")
	show("Jan 2, 2006", "Jul 20, 2026")
	show("January 2, 2006", "July 20, 2026")
	show("Mon Jan 2 15:04:05 2006", "Mon Jul 20 14:30:15 2026")
	show("3:04 PM", "2:30 PM")
	show("03:04:05 pm", "02:30:15 pm")
	show("2006-01-02T15:04:05.000", "2026-07-20T14:30:15.250")
	show("06/1/2", "26/7/20")
	show("_2 Jan 2006", "20 Jul 2026")
	show("2006-01-02 15:04:05 -0700", "2026-01-05 09:08:07 +0530")
	show("15:04:05.999999999", "14:30:15.5")
}
`,
		},
		{
			"bad values report Go's ParseError",
			`package main

import (
	"fmt"
	"time"
)

func showErr(layout, value string) {
	_, err := time.Parse(layout, value)
	fmt.Println(err)
}

func main() {
	showErr("2006-01-02", "2026-13-20")
	showErr("2006-01-02", "2026-07-40")
	showErr("2006-01-02 15:04:05", "2026-07-20 25:00:00")
	showErr("2006-01-02 15:04:05", "2026-07-20 14:61:00")
	showErr("2006-01-02", "notadate")
	showErr("2006-01-02", "2026-07-20 extra")
	showErr("3:04 PM", "2:30 ZZ")
	showErr("2006-01-02", "2021-02-29")
}
`,
		},
		{
			"format then parse round-trips",
			`package main

import (
	"fmt"
	"time"
)

func roundTrip(orig time.Time, layout string) {
	s := orig.Format(layout)
	back, err := time.Parse(layout, s)
	fmt.Println(err == nil, back.Format(time.RFC3339))
}

func main() {
	orig := time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)
	roundTrip(orig, time.RFC3339)
	roundTrip(orig, "2006-01-02 15:04:05")
	roundTrip(orig, "Jan 2, 2006 3:04:05 PM")

	jst := time.FixedZone("JST", 9*3600)
	zoned := time.Date(2026, 1, 5, 9, 8, 7, 0, jst)
	s := zoned.Format(time.RFC3339)
	back, _ := time.Parse(time.RFC3339, s)
	fmt.Println(zoned.Equal(back), back.Format(time.RFC3339))
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}
