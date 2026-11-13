// Package xuse: cross-package probes through imported facts.
package xuse

import "xlib"

// X1: both sides call the lazy getter: two unsynchronised writes of cache.
func X1() {
	t := &xlib.T{}
	done := make(chan bool)
	go func() { _ = t.Get(); done <- true }()
	_ = t.Get() // race
	<-done
}

// X2: control: both sides only read.
func X2() {
	t := &xlib.T{}
	done := make(chan bool)
	go func() { _ = t.Size(); done <- true }()
	_ = t.Size()
	<-done
}

// X3: field-projected Sync: the goroutine uses the self-locked Hit, the parent writes Plain.
func X3() {
	t := &xlib.T{}
	done := make(chan bool)
	go func() { t.Hit(); t.Plain++; done <- true }()
	t.Plain = 1 // race on Plain
	<-done
}

// X4: field-projected Sync: both sides call Hit only. Race-free.
func X4() {
	t := &xlib.T{}
	done := make(chan bool)
	go func() { t.Hit(); done <- true }()
	t.Hit()
	<-done
}
