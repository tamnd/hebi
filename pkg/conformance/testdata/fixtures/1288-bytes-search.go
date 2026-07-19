package main

import (
	"bytes"
	"fmt"
)

func main() {
	b := []byte("go gopher")
	fmt.Println(bytes.Contains(b, []byte("go")), bytes.HasPrefix(b, []byte("go")), bytes.HasSuffix(b, []byte("her")))
	fmt.Println(bytes.Index(b, []byte("go")), bytes.LastIndex(b, []byte("go")), bytes.IndexByte(b, 'p'))
	fmt.Println(bytes.Count(b, []byte("g")), bytes.Equal(b, []byte("go gopher")))
	fmt.Println(bytes.Compare([]byte("ab"), []byte("abc")), bytes.Compare([]byte("b"), []byte("a")))
}
