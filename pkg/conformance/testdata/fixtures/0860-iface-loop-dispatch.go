package main

import "fmt"

type Stringer interface{ String() string }

type tag struct{ s string }

func (t tag) String() string { return t.s }

func main() {
	xs := []Stringer{tag{"a"}, tag{"b"}, tag{"c"}}
	total := ""
	for i := 0; i < len(xs); i++ {
		total += xs[i].String()
	}
	fmt.Println(total)
}
