package main

import "fmt"

type Scale struct {
	K int
}

func (s Scale) Apply(n int) int {
	return s.K * n
}

func main() {
	f := Scale.Apply
	s := Scale{K: 3}
	fmt.Println(f(s, 5))
}
