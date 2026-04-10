package diff_test

import (
	"testing"

	"go.followtheprocess.codes/diff"
)

func TestHello(t *testing.T) {
	got := diff.Hello()
	want := "Hello diff"

	if got != want {
		t.Errorf("got %s, wanted %s", got, want)
	}
}
