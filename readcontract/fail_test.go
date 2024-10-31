package main

import (
	"errors"
	"os"
	"sync"
	"testing"
)

func TestFail(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	t.Fatal("fake failure")
}

func TestRace(t *testing.T) {
	var v int
	var wg sync.WaitGroup
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func() {
			defer wg.Done()
			v++
			v--
		}()
	}
	wg.Wait()
	t.Log(v)
}

func TestLint(t *testing.T) {
	const v1 = (true && false) && (true && false) // SQ Identical expressions should not be used on both sides of a binary operator
	a := 1
	if !(a == 2) { // SQ boolean check should not be inverted
	}
	const UnusedVar = 1 // lint should complain for unused variable
	const ALL_CAPS = 10 // should be AllCaps
	err := os.ErrNotExist
	if err == os.ErrNotExist { // should use errors.Is
		err := errors.New("fake error") // shadowed variable
		t.Log(err)
	}
}
