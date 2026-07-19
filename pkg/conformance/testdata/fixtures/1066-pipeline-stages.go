package main

import "fmt"

func main() {
	gen := make(chan int)
	sq := make(chan int)
	go func() {
		for i := 1; i <= 5; i++ {
			gen <- i
		}
		close(gen)
	}()
	go func() {
		for n := range gen {
			sq <- n * n
		}
		close(sq)
	}()
	total := 0
	for v := range sq {
		total += v
	}
	fmt.Println(total)
}
