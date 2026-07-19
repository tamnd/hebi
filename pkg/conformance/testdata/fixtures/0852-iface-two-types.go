package main

import "fmt"

type Shape interface{ Area() int }

type Square struct{ Side int }

func (s Square) Area() int { return s.Side * s.Side }

type Rect struct{ W, H int }

func (r Rect) Area() int { return r.W * r.H }

func main() {
	shapes := []Shape{Square{3}, Rect{2, 5}}
	for i := 0; i < len(shapes); i++ {
		fmt.Println(shapes[i].Area())
	}
}
