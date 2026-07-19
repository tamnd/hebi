package main

import "fmt"

type Reg struct {
	V int
}

func (r *Reg) Set(x int) {
	r.V = x
}

func main() {
	r := &Reg{}
	r.Set(55)
	fmt.Println(r.V)
}
