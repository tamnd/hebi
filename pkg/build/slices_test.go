package build

import (
	"testing"
)

// TestSlicesFuncs checks the slices package lowering against go run. Each
// program exercises a family of the mapped functions, including the value
// searches, the in-place Sort and Reverse and Compact, SortFunc with a caller
// comparator, and the (index, found) BinarySearch, held byte for byte against
// Go's output.
func TestSlicesFuncs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"contains index and func variants",
			`package main

import (
	"fmt"
	"slices"
)

func main() {
	a := []int{10, 20, 30, 40}
	fmt.Println(slices.Contains(a, 30), slices.Contains(a, 99))
	fmt.Println(slices.Index(a, 20), slices.Index(a, 99))
	fmt.Println(slices.IndexFunc(a, func(x int) bool { return x > 25 }))
	fmt.Println(slices.ContainsFunc(a, func(x int) bool { return x%7 == 0 }))
}
`,
		},
		{
			"sort max min reverse equal",
			`package main

import (
	"fmt"
	"slices"
)

func main() {
	a := []int{4, 1, 3, 2}
	slices.Sort(a)
	fmt.Println(a, slices.Max(a), slices.Min(a))
	slices.Reverse(a)
	fmt.Println(a)
	fmt.Println(slices.Equal(a, []int{4, 3, 2, 1}), slices.Equal(a, []int{1, 2}))
}
`,
		},
		{
			"sortfunc clone compact binarysearch",
			`package main

import (
	"fmt"
	"slices"
)

func main() {
	a := []int{3, 1, 2, 1, 3}
	b := slices.Clone(a)
	slices.SortFunc(b, func(x, y int) int { return y - x })
	fmt.Println(a, b)
	c := []int{1, 1, 2, 3, 3, 3, 4}
	c = slices.Compact(c)
	fmt.Println(c)
	d := []int{2, 4, 6, 8}
	i, ok := slices.BinarySearch(d, 6)
	fmt.Println(i, ok)
	i, ok = slices.BinarySearch(d, 5)
	fmt.Println(i, ok)
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}
