package main

import "fmt"

type Adder interface{ Add(int) }

type Acc struct{ sum int }

func (a *Acc) Add(n int) { a.sum = a.sum + n }

func main() {
	a := &Acc{}
	var ad Adder = a
	ad.Add(3)
	ad.Add(4)
	fmt.Println(a.sum)
}
