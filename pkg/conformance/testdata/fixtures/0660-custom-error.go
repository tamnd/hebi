package main

import "fmt"

type MyErr struct {
	Msg string
}

func (e *MyErr) Error() string {
	return "boom: " + e.Msg
}

func main() {
	var e error = &MyErr{Msg: "kaboom"}
	fmt.Println(e)
}
