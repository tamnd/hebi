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
	u := User{Base{7}, "a"}
	u.ID = 9
	fmt.Println(u.ID, u.Base.ID)
}
