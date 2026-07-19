package main

import "fmt"

func main() {
	ping := make(chan int)
	pong := make(chan int)
	go func() {
		for {
			n, ok := <-ping
			if !ok {
				close(pong)
				return
			}
			pong <- n * 2
		}
	}()
	out := 0
	go func() {
		for i := 1; i <= 4; i++ {
			ping <- i
		}
		close(ping)
	}()
	for v := range pong {
		out += v
	}
	fmt.Println(out)
}
