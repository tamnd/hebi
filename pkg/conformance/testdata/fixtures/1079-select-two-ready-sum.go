package main

import "fmt"

func main() {
	a := make(chan int, 1)
	b := make(chan int, 1)
	a <- 3
	b <- 4
	total := 0
	for range 2 {
		select {
		case v := <-a:
			total += v
		case v := <-b:
			total += v
		}
	}
	fmt.Println(total)
}
