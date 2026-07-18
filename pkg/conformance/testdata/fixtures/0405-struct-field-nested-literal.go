package main

import "fmt"

type Grid struct {
	Cells [2]int
}

func main() {
	g := Grid{Cells: [2]int{7, 8}}
	fmt.Println(g.Cells)
}
