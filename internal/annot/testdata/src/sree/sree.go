package sree

//gosree:iso p
func sreeIso(p *int) {}

//gosree:iso p
func shortSreeIso(p *int) {}

//gosree:imm
type SreeImm struct{ n int }

//gosree:imm
type ShortSreeImm struct{ n int }

//gosree:mub p
func sreeMub(p *int) { *p = 1 }

//gosree:rob p
func shortSreeRob(p *int) int { return *p }

func sreeLocals() {
	x := 10
	//gosree:mut
	b1 := &x
	b2 := &x //gosree:rob
	/*gosree:mut*/ b3 := &x
	b4 := /*gosree:rob*/ &x
	//gosree:unsafe testing sree suppression
	_ = b1
	//gosree:unsafe testing short sree suppression
	_ = b2
	_ = b3
	_ = b4
}

//gosree:bogus // want `SAFE007: unknown directive //gosree:bogus`
func badSreeDirective() {}
