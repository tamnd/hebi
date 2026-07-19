package main

import "fmt"

type point struct{ x, y int }

func main() {
	ch := make(chan point)
	go func() { ch <- point{3, 4} }()
	p := <-ch
	fmt.Println(p.x + p.y)
}
