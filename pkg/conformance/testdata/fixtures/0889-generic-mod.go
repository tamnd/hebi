package main

import "fmt"

func Mod[T int | int64](a, b T) T { return a % b }

func main() {
	fmt.Println(Mod(-7, 3), Mod(7, 3))
}
