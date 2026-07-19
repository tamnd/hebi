package main

import "fmt"

func Div[T int | float64](a, b T) T { return a / b }

func main() {
	fmt.Println(Div(7, 2), Div(7.0, 2.0))
}
