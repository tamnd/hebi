package main

import "fmt"

func Sum[T int | float64](xs []T) T {
	total := xs[0]
	for i := 1; i < len(xs); i++ {
		total += xs[i]
	}
	return total
}

func main() {
	fmt.Println(Sum([]int{1, 2, 3}), Sum([]float64{1.5, 2.5}))
}
