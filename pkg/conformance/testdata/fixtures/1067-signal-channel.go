package main

import "fmt"

func main() {
	ready := make(chan struct{})
	order := make(chan string, 2)
	go func() {
		<-ready
		order <- "worker"
	}()
	order <- "main"
	close(ready)
	first := <-order
	second := <-order
	fmt.Println(first, second)
}
