package build

import (
	"testing"
)

// TestSortFuncs checks the sort package lowering against go run. Each program
// exercises a family of the mapped functions, including the less-driven Slice
// and SliceStable and the binary searches, and the differential harness holds
// the compiled output byte for byte against Go's.
func TestSortFuncs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"typed sorts",
			`package main

import (
	"fmt"
	"sort"
)

func main() {
	a := []int{5, 2, 4, 1, 3}
	sort.Ints(a)
	fmt.Println(a, sort.IntsAreSorted(a))
	f := []float64{3.5, 1.2, 2.8}
	sort.Float64s(f)
	fmt.Println(f)
	s := []string{"pear", "apple", "orange"}
	sort.Strings(s)
	fmt.Println(s, sort.StringsAreSorted(s))
}
`,
		},
		{
			"searches",
			`package main

import (
	"fmt"
	"sort"
)

func main() {
	a := []int{1, 3, 5, 7, 9}
	fmt.Println(sort.SearchInts(a, 5), sort.SearchInts(a, 6), sort.SearchInts(a, 0))
	s := []string{"a", "c", "e"}
	fmt.Println(sort.SearchStrings(s, "c"), sort.SearchStrings(s, "d"))
	n := sort.Search(100, func(i int) bool { return i*i >= 50 })
	fmt.Println(n)
}
`,
		},
		{
			"slice with less",
			`package main

import (
	"fmt"
	"sort"
)

type person struct {
	Name string
	Age  int
}

func main() {
	people := []person{{"Al", 30}, {"Bo", 20}, {"Cy", 25}}
	sort.Slice(people, func(i, j int) bool { return people[i].Age < people[j].Age })
	fmt.Println(people)
	nums := []int{5, 3, 8, 1}
	sort.SliceStable(nums, func(i, j int) bool { return nums[i] > nums[j] })
	fmt.Println(nums, sort.SliceIsSorted(nums, func(i, j int) bool { return nums[i] > nums[j] }))
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
