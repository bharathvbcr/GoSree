package a

//safe:bogus // want `SAFE007: unknown directive //safe:bogus`
func unknown() {}

//safe:iso q // want `SAFE007: //safe:iso names "q", which is not a parameter of misnamed`
func misnamed(p *int) { _ = p }

//safe:iso p
func named(p *int) { _ = p }

func missingReason() {
	//safe:unsafe // want `SAFE007: //safe:unsafe needs a reason`
	_ = 1
}

//safe:imm // want `SAFE007: //safe:imm must be in a type declaration's doc comment`
func notAType() {}

//safe:imm
type T struct{ n int }

func dangling() {
	_ = 1
	//safe:unsafe nothing follows // want `SAFE007: //safe:unsafe is not attached to a statement or declaration`
}
