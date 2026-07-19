package main

import "fmt"

type Stringer interface{ String() string }

type Pt struct{ X, Y int }

func (p Pt) String() string { return "pt" }

func Show[T Stringer](x T) string { return x.String() }

func main() {
	fmt.Println(Show(Pt{1, 2}))
}
