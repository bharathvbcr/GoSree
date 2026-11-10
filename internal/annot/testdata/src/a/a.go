package a

//gosree:bogus // want `SAFE007: unknown directive //gosree:bogus`
func unknown() {}

//gosree:iso q // want `SAFE007: //gosree:iso names "q", which is not a parameter of misnamed`
func misnamed(p *int) { _ = p }

//gosree:iso p
func named(p *int) { _ = p }

func missingReason() {
	//gosree:unsafe // want `SAFE007: //gosree:unsafe needs a reason`
	_ = 1
}

//gosree:imm // want `SAFE007: //gosree:imm must be in a type declaration's doc comment`
func notAType() {}

//gosree:imm
type T struct{ n int }

func dangling() {
	_ = 1
	//gosree:unsafe nothing follows // want `SAFE007: //gosree:unsafe is not attached to a statement or declaration`
}
