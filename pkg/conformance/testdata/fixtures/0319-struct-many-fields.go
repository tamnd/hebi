package main

import "fmt"

type Rec struct {
	A int
	B int
	C int
	D bool
}

func main() {
	r := Rec{A: 1, C: 3, D: true}
	fmt.Println(r.A, r.B, r.C, r.D)
}
