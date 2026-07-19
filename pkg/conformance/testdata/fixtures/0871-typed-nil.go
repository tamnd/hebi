package main

import "fmt"

type E interface{ Error() string }

type myErr struct{}

func (*myErr) Error() string { return "e" }

func get() E {
	var p *myErr
	return p
}

func main() {
	err := get()
	fmt.Println(err == nil)
}
