package main

import "fmt"

func main() {
	msg := "first"
	defer func() {
		fmt.Println(msg)
	}()
	msg = "second"
}
