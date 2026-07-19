package build

import (
	"testing"
)

// TestSelect checks the select surface against go run. A select with a default
// is non-blocking and takes the default when nothing is ready, a select without a
// default blocks until a case can proceed and picks among several producers, a
// send case deposits into a channel with room, and a comma-ok receive case sees a
// closed channel report ok false.
func TestSelect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"a default makes a select non-blocking",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int)\n\tselect {\n\tcase v := <-ch:\n\t\tfmt.Println(v)\n\tdefault:\n\t\tfmt.Println(\"idle\")\n\t}\n}\n",
		},
		{
			"a buffered value is received rather than the default",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int, 1)\n\tch <- 7\n\tselect {\n\tcase v := <-ch:\n\t\tfmt.Println(\"got\", v)\n\tdefault:\n\t\tfmt.Println(\"idle\")\n\t}\n}\n",
		},
		{
			"a blocking select drains two producers",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := make(chan int)\n\tb := make(chan int)\n\tgo func() { a <- 1 }()\n\tgo func() { b <- 2 }()\n\tsum := 0\n\tfor i := 0; i < 2; i++ {\n\t\tselect {\n\t\tcase v := <-a:\n\t\t\tsum += v\n\t\tcase v := <-b:\n\t\t\tsum += v\n\t\t}\n\t}\n\tfmt.Println(sum)\n}\n",
		},
		{
			"a send case deposits into a channel with room",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tc := make(chan string, 1)\n\tselect {\n\tcase c <- \"hi\":\n\t\tfmt.Println(\"sent\")\n\tdefault:\n\t\tfmt.Println(\"full\")\n\t}\n\tfmt.Println(<-c)\n}\n",
		},
		{
			"a comma-ok receive case sees a closed channel",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\td := make(chan int)\n\tclose(d)\n\tselect {\n\tcase v, ok := <-d:\n\t\tfmt.Println(v, ok)\n\t}\n}\n",
		},
		{
			"an unbuffered send case reaches a waiting receiver",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int)\n\tdone := make(chan bool)\n\tgo func() {\n\t\tv := <-ch\n\t\tfmt.Println(\"received\", v)\n\t\tdone <- true\n\t}()\n\tsent := false\n\tfor !sent {\n\t\tselect {\n\t\tcase ch <- 99:\n\t\t\tsent = true\n\t\tdefault:\n\t\t}\n\t}\n\t<-done\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestSelectFairness pins that a select over several ready cases chooses uniformly
// at random, the property Go promises, tested as a distribution rather than an
// exact sequence: two always-ready channels each win close to half of many
// selects, so both counts land in a wide tolerance band. The printed verdict is
// stable across runs even though the choice is random, so it diffs against go run.
func TestSelectFairness(t *testing.T) {
	t.Parallel()
	src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\ta := make(chan int, 1)\n\tb := make(chan int, 1)\n\ta <- 1\n\tb <- 1\n\tca, cb := 0, 0\n\tfor i := 0; i < 2000; i++ {\n\t\tselect {\n\t\tcase <-a:\n\t\t\tca++\n\t\t\ta <- 1\n\t\tcase <-b:\n\t\t\tcb++\n\t\t\tb <- 1\n\t\t}\n\t}\n\t<-a\n\t<-b\n\tfmt.Println(ca > 700 && ca < 1300 && cb > 700 && cb < 1300)\n}\n"
	assertProgramMatchesGo(t, src)
}

// TestSelectEmit pins the emitted shape: the builder call carries the has-default
// flag and each case as a tuple tagged 0 for a receive and 1 for a send, and the
// run result dispatches through an index test.
func TestSelectEmit(t *testing.T) {
	t.Parallel()
	src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tin := make(chan int)\n\tout := make(chan int, 1)\n\tselect {\n\tcase v := <-in:\n\t\tfmt.Println(v)\n\tcase out <- 1:\n\t\tfmt.Println(\"sent\")\n\tdefault:\n\t\tfmt.Println(\"idle\")\n\t}\n}\n"
	got := emitOf(t, src)
	for _, want := range []string{
		"_hebirt.select(True, (0, in), (1, out, 1))",
		"if (_sel == 0):",
	} {
		if !bytesContains(got, want) {
			t.Errorf("select emit missing %q:\n%s", want, got)
		}
	}
}

// TestSelectSendOnClosedCrashes pins that a select send case that fires on a
// closed channel crashes the Go way, the panic escaping main and exiting two.
func TestSelectSendOnClosedCrashes(t *testing.T) {
	t.Parallel()
	assertProgramCrashesLikeGo(t, "package main\n\nfunc main() {\n\tch := make(chan int, 1)\n\tclose(ch)\n\tselect {\n\tcase ch <- 1:\n\t}\n}\n")
}
