package main

import "fmt"

type Named struct {
	Name string
}

func (n Named) Get() string {
	return n.Name
}

func main() {
	n := Named{Name: "go"}
	fmt.Println(n.Get())
}
