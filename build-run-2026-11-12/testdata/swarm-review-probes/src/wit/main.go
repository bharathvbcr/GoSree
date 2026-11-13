// Command wit runs one lost shape (os.Args[1]) under -race to prove it is a real race.
package main

import (
	"os"
	"sync"
	"time"
)

type Job struct{ ID int }

func d12() {
	j := &Job{}
	ch := make(chan *Job, 1)
	go func() {
		x := <-ch
		x.ID = 5
	}()
	done := make(chan bool)
	go func() { time.Sleep(10 * time.Millisecond); _ = j.ID; done <- true }()
	ch <- j
	<-done
}

func e1(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[0]
		wg.Go(func() { it.ID = i })
	}
	wg.Wait()
}

func e2(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[i/2]
		wg.Go(func() { it.ID = i })
	}
	wg.Wait()
}

func e4(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := range items {
		for k := 0; k < 2; k++ {
			it := &items[i]
			wg.Go(func() { it.ID = k })
		}
	}
	wg.Wait()
}

func e6(n int) {
	items := make([]Job, n+1)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		it := &items[i]
		nx := &items[i+1]
		wg.Go(func() { it.ID = nx.ID })
	}
	wg.Wait()
}

func main() {
	switch os.Args[1] {
	case "d12":
		d12()
	case "e1":
		e1(4)
	case "e2":
		e2(4)
	case "e4":
		e4(4)
	case "e6":
		e6(4)
	}
}
