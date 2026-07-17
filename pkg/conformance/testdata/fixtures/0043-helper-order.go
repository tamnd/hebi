package main

import "fmt"

func middle() {
	fmt.Println("middle")
}

func main() {
	fmt.Println("start")
	middle()
	fmt.Println("end")
}
