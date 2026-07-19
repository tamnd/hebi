package main

import "fmt"

func main() {
	done := make(chan string)
	go func() {
		defer func() {
			r := recover()
			if r != nil {
				done <- "recovered " + r.(string)
			}
		}()
		panic("boom")
	}()
	fmt.Println(<-done)
}
