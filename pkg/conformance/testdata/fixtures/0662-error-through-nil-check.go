package main

import "fmt"

type OpenErr struct {
	Path string
}

func (e *OpenErr) Error() string {
	return "cannot open " + e.Path
}

func open(bad bool) error {
	if bad {
		return &OpenErr{Path: "/x"}
	}
	return nil
}

func main() {
	err := open(true)
	if err != nil {
		fmt.Println(err)
	}
	ok := open(false)
	if ok == nil {
		fmt.Println("ok")
	}
}
