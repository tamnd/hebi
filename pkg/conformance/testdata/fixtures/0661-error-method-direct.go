package main

import "fmt"

type MyErr struct {
	Code int
}

func (e *MyErr) Error() string {
	return "code error"
}

func main() {
	e := &MyErr{Code: 7}
	fmt.Println(e.Error())
}
