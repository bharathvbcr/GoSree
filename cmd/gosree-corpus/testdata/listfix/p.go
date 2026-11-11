package p

// Stale: mutes nothing, so the stale analyzer reports it as SAFE007.
func plain() int {
	//gosree:unsafe nothing here needs it
	return 1
}

//gosree:bogus x
func malformed() int { return 2 }
