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
	u := User{Base{5}, "a"}
	p := &u.ID
	*p = 9
	fmt.Println(u.ID)
}
