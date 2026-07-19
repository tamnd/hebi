package main

import "fmt"

func main() {
	done := make(chan struct{})
	close(done)
	select {
	case <-done:
		fmt.Println("closed wins")
	default:
		fmt.Println("default")
	}
}
