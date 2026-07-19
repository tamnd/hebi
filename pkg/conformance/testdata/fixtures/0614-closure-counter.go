package main

import "fmt"

func main() {
	count := 0
	bump := func() {
		count++
	}
	bump()
	bump()
	bump()
	fmt.Println(count)
}
