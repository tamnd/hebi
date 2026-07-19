package main

import "fmt"

type Num struct {
	V int
}

func (n Num) Plus(x int) Num {
	return Num{V: n.V + x}
}

func main() {
	n := Num{V: 0}
	m := n.Plus(2).Plus(3).Plus(4)
	fmt.Println(m.V)
}
