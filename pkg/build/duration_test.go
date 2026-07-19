package build

import (
	"testing"
)

// TestTimeDuration checks time.Duration against go run. A Duration boxes into the
// runtime Duration class, an int64 subclass, so the unit constants scale, the
// arithmetic reboxes, the accessors return the Go counts and fractions, and String
// renders the way Go's fmt does across the sub-second and hour-minute-second forms.
func TestTimeDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"unit constants and string forms",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println(time.Duration(0))
	fmt.Println(500 * time.Nanosecond)
	fmt.Println(1500 * time.Microsecond)
	fmt.Println(1500 * time.Millisecond)
	fmt.Println(2 * time.Second)
	fmt.Println(90 * time.Minute)
	fmt.Println(time.Hour + 30*time.Minute + 500*time.Millisecond)
	fmt.Println(-90 * time.Minute)
}
`,
		},
		{
			"accessors",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	d := 2 * time.Second
	fmt.Println(d.Nanoseconds(), d.Microseconds(), d.Milliseconds())
	fmt.Println(d.Seconds(), d.Minutes(), d.Hours())
	h := 3 * time.Hour
	fmt.Println(h.Hours(), h.Minutes())
}
`,
		},
		{
			"round and truncate",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	d := 2500 * time.Millisecond
	fmt.Println(d.Round(time.Second))
	fmt.Println(d.Truncate(time.Second))
	fmt.Println((1400 * time.Millisecond).Round(time.Second))
	fmt.Println((-2500 * time.Millisecond).Round(time.Second))
}
`,
		},
		{
			"compound assignment reboxes",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	total := time.Duration(0)
	for i := 0; i < 3; i++ {
		total += time.Duration(i) * time.Second
	}
	fmt.Println(total)
}
`,
		},
		{
			"method value and expression",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	d := 3 * time.Hour
	f := d.Hours
	fmt.Println(f())
	fmt.Println(time.Duration.Seconds(d))
}
`,
		},
		{
			"struct field and comparison",
			`package main

import (
	"fmt"
	"time"
)

type Job struct {
	Timeout time.Duration
}

func main() {
	j := Job{Timeout: 90 * time.Second}
	fmt.Println(j.Timeout, j.Timeout.Minutes())
	if j.Timeout > time.Minute {
		fmt.Println("over a minute")
	}
	fmt.Println(j.Timeout == 90*time.Second)
}
`,
		},
		{
			"slice of durations prints",
			`package main

import (
	"fmt"
	"time"
)

func main() {
	durs := []time.Duration{time.Second, 2 * time.Minute, 500 * time.Millisecond}
	fmt.Println(durs)
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
