package main

import "fmt"

type Stringer interface{ String() string }

type P struct{}

func (P) String() string { return "p" }

func main() {
	var x interface{} = P{}
	s, ok := x.(Stringer)
	fmt.Println(s.String(), ok)
}
