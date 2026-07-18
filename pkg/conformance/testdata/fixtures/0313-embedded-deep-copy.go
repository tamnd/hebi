package main

import "fmt"

type Base struct {
	ID int
}

type User struct {
	Base
	Name string
}

func main() {
	a := User{Base{1}, "x"}
	b := a
	b.ID = 2
	fmt.Println(a.ID, b.ID)
}
