package main

import "fmt"

type temp struct{ v int }

func (t temp) String() string { return fmt.Sprintf("%dK", t.v) }

func main() {
	t := temp{300}
	fmt.Printf("%v %s\n", t, t)
}
