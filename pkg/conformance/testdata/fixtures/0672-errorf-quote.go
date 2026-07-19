package main

import "fmt"

func main() {
	err := fmt.Errorf("bad key %q", "a\tb")
	fmt.Println(err)
}
