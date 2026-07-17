package main

import "fmt"

func main() {
	s := "go"
	switch s {
	case "py":
		fmt.Println("python")
	case "go":
		fmt.Println("golang")
	}
}
