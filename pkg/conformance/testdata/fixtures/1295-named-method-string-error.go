package main

import (
	"fmt"
	"strings"
)

type Name string

func (n Name) Shout() string {
	return strings.ToUpper(string(n)) + "!"
}

type Code int

func (c Code) Error() string {
	return fmt.Sprintf("code %d", int(c))
}

func main() {
	n := Name("hebi")
	fmt.Println(n.Shout(), len(n))

	var err error = Code(42)
	fmt.Println(err)
	fmt.Printf("%v\n", err)
}
