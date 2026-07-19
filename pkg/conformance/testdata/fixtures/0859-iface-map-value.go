package main

import "fmt"

type Greeter interface{ Hello() string }

type en struct{}

func (en) Hello() string { return "hello" }

func main() {
	m := map[string]Greeter{"e": en{}}
	fmt.Println(m["e"].Hello())
}
