package main

import "fmt"

type Speaker interface{ Speak() string }

type Dog struct{}

func (Dog) Speak() string { return "woof" }

func main() {
	var s Speaker = Dog{}
	fmt.Println(s.Speak())
}
