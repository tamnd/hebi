package main

import "fmt"

type Counter interface{ Value() int }

type box struct{ v int }

func (b box) Value() int { return b.v }

func make1() Counter { return box{42} }

func main() {
	fmt.Println(make1().Value())
}
