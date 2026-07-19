package main

import "fmt"

type Celsius struct {
	Deg int
}

func (c Celsius) Doubled() int {
	return c.Deg * 2
}

func main() {
	c := Celsius{Deg: 21}
	fmt.Println(c.Doubled())
}
