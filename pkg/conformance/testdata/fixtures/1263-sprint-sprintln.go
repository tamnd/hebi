package main

import "fmt"

func main() {
	fmt.Print(fmt.Sprint("x", 1, "y", 2))
	fmt.Println()
	fmt.Print(fmt.Sprintln("done", 1, true))
}
