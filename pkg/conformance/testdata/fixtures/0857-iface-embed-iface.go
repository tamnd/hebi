package main

import "fmt"

type Reader interface{ Read() string }

type Writer interface{ Write(string) }

type ReadWriter interface {
	Reader
	Writer
}

type buf struct{ data string }

func (b *buf) Read() string   { return b.data }
func (b *buf) Write(s string) { b.data = s }

func main() {
	var rw ReadWriter = &buf{}
	rw.Write("hello")
	fmt.Println(rw.Read())
}
