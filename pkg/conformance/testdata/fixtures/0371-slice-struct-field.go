package main

import "fmt"

type Bag struct {
	Items []int
}

func main() {
	b := Bag{Items: []int{1, 2, 3}}
	c := b
	c.Items[0] = 99
	fmt.Println(b.Items[0], c.Items[0])
}
