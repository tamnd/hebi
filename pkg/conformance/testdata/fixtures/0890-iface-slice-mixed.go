package main

import "fmt"

type Animal interface{ Sound() string }

type Cow struct{}

func (Cow) Sound() string { return "moo" }

type Duck struct{}

func (Duck) Sound() string { return "quack" }

func main() {
	zoo := []Animal{Cow{}, Duck{}, Cow{}}
	out := ""
	for i := 0; i < len(zoo); i++ {
		out += zoo[i].Sound() + " "
	}
	fmt.Println(out)
}
