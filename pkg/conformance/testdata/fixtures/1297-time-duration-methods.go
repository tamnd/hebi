package main

import (
	"fmt"
	"time"
)

type Job struct {
	Timeout time.Duration
}

func main() {
	d := 2 * time.Second
	fmt.Println(d.Nanoseconds(), d.Microseconds(), d.Milliseconds())
	fmt.Println(d.Seconds(), d.Minutes(), d.Hours())

	fmt.Println((2500 * time.Millisecond).Round(time.Second))
	fmt.Println((2500 * time.Millisecond).Truncate(time.Second))

	total := time.Duration(0)
	for i := 0; i < 3; i++ {
		total += time.Duration(i) * time.Second
	}
	fmt.Println(total)

	j := Job{Timeout: 90 * time.Second}
	fmt.Println(j.Timeout, j.Timeout.Minutes())
	if j.Timeout > time.Minute {
		fmt.Println("over a minute")
	}
}
