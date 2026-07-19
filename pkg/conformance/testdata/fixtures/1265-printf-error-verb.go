package main

import "fmt"

type myErr struct{ code int }

func (e myErr) Error() string { return fmt.Sprintf("err %d", e.code) }

func main() {
	var e error = myErr{7}
	fmt.Printf("%v | %s\n", e, e)
}
