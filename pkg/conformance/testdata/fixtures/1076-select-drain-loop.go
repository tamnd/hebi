package main

import "fmt"

func main() {
	ch := make(chan int, 4)
	for i := 1; i <= 4; i++ {
		ch <- i
	}
	sum := 0
	for {
		select {
		case v := <-ch:
			sum += v
		default:
			fmt.Println(sum)
			return
		}
	}
}
