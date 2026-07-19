package main

import "fmt"

type Engine struct{ hp int }

func (e Engine) Power() int { return e.hp }

type Car struct {
	Engine
	name string
}

func main() {
	c := Car{Engine{120}, "z"}
	fmt.Println(c.Power(), c.name)
}
