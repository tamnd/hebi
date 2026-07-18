package main

import "fmt"

type Bag struct {
	Items []int
}

func main() {
	b := Bag{Items: []int{9, 8, 7}}
	fmt.Println(b.Items, len(b.Items))
}
