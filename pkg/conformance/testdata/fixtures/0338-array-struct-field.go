package main

import "fmt"

type Grid struct {
	Cells [3]int
}

func main() {
	g := Grid{Cells: [3]int{7, 8, 9}}
	h := g
	h.Cells[0] = 1
	fmt.Println(g.Cells[0], h.Cells[0])
}
