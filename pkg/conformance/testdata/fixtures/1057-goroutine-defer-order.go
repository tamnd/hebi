package main

import "fmt"

func main() {
	done := make(chan string)
	go func() {
		out := ""
		defer func() { done <- out }()
		defer func() { out += "b" }()
		out += "a"
	}()
	fmt.Println(<-done)
}
