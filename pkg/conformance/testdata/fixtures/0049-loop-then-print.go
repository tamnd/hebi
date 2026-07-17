package main

import "fmt"

func main() {
	n := 0
	count := 0
	for n < 10 {
		count = count + 1
		n = n + 2
	}
	fmt.Println(count)
}
