package main

import "fmt"

func cleanup(name string) {
	fmt.Println("cleaning", name)
}

func main() {
	defer cleanup("a")
	defer cleanup("b")
	fmt.Println("working")
}
