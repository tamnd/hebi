package main

import "fmt"

func main() {
	steps := make(chan int)
	go func() {
		for i := 1; i <= 5; i++ {
			steps <- i
		}
		close(steps)
	}()
	sum := 0
	for s := range steps {
		sum += s
	}
	fmt.Println(sum)
}
