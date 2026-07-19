package main

import "fmt"

type Named interface{ Name() string }

type Cat struct{ n string }

func (c Cat) Name() string { return c.n }

func greet(n Named) string { return "hi " + n.Name() }

func main() {
	fmt.Println(greet(Cat{"tom"}))
}
