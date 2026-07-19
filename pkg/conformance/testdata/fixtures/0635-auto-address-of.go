package main

import "fmt"

type Acc struct {
	Sum int
}

func (a *Acc) Add(x int) {
	a.Sum = a.Sum + x
}

func main() {
	a := Acc{}
	a.Add(3)
	a.Add(4)
	fmt.Println(a.Sum)
}
