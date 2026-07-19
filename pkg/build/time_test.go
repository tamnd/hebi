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
