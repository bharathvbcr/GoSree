package h

// Each statement with an "in" or "out" comment on its line is, or is not, covered by a hatch.

func trailingHeader(n int) {
	if n > 0 { //gosree:unsafe the header only
		_ = n // out
	}
	for i := 0; i < n; i++ { //gosree:unsafe the header only
		_ = i // out
	}
	switch n { //gosree:unsafe the tag only
	case 1:
		_ = n // out
	}
	switch n {
	case 2: //gosree:unsafe the case expression only
		_ = n // out
	}
	go func() { //gosree:unsafe the go statement only
		_ = n // out
	}()
}

func ownLine(n int) {
	//gosree:unsafe the whole if
	if n > 0 {
		_ = n // in
	}
	_ = n // out
}

func oneLine(n int) {
	f := func() int { return n } //gosree:unsafe one line
	_ = f                        // out
}

//gosree:unsafe the whole function, goroutines included
func declLevel(n int) {
	_ = n // in
	go func() {
		_ = n // in
	}()
}

func multiLine(n int) {
	_ = []int{ //gosree:unsafe a statement without blocks, all of it
		n, // in
	}
}
