package main

import "fmt"

func counter(n int) <-chan int {
	ch := make(chan int)
	go func() {
		for i := 0; i < n; i++ {
			ch <- i
		}
		close(ch)
	}()
	return ch
}

func main() {
	sum := 0
	for v := range counter(6) {
		sum += v
	}
	fmt.Println(sum)
}
