package main

import "fmt"

type Greeter struct {
	Name string
}

func (g Greeter) Hi() string {
	return "hi " + g.Name
}

func main() {
	g := Greeter{Name: "x"}
	f := g.Hi
	fmt.Println(f())
}
