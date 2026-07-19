package main

import "fmt"

func main() {
	done := make(chan struct{})
	value := 0
	go func() {
		value = 42
		close(done)
	}()
	<-done
	fmt.Println(value)
}
