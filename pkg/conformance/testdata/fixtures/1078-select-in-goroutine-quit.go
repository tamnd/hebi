package main

import "fmt"

func main() {
	work := make(chan int)
	quit := make(chan struct{})
	result := make(chan int)
	go func() {
		total := 0
		for {
			select {
			case v := <-work:
				total += v
			case <-quit:
				result <- total
				return
			}
		}
	}()
	for i := 1; i <= 5; i++ {
		work <- i
	}
	close(quit)
	fmt.Println(<-result)
}
