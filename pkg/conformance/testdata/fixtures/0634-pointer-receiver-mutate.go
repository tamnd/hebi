package main

import "fmt"

type Counter struct {
	N int
}

func (c *Counter) Inc() {
	c.N = c.N + 1
}

func main() {
	c := Counter{}
	c.Inc()
	c.Inc()
	fmt.Println(c.N)
}
