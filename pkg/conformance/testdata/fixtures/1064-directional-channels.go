package main

import "fmt"

func produce(out chan<- int) {
	for i := 1; i <= 5; i++ {
		out <- i
	}
	close(out)
}

func consume(src <-chan int, done chan<- int) {
	total := 0
	for v := range src {
		total += v
	}
	done <- total
}

func main() {
	ch := make(chan int)
	done := make(chan int)
	go produce(ch)
	go consume(ch, done)
	fmt.Println(<-done)
}
