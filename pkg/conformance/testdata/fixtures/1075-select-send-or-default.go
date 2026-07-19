package main

import "fmt"

func main() {
	ch := make(chan int)
	sent := false
	select {
	case ch <- 1:
		sent = true
	default:
	}
	fmt.Println(sent)
}
