package main

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

	d := 2 * time.Second
	fmt.Printf("%v %s %d\n", d, d, int64(d))
}
