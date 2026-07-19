package main

import "fmt"

func safe() {
	defer func() {
		recover()
	}()
	panic("boom")
}

func main() {
	safe()
	fmt.Println("after")
}
