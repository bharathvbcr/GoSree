package sree

//gosree:iso p
func sreeIso(p *int) {}

//sree:iso p
func shortSreeIso(p *int) {}

//gosree:imm
type SreeImm struct{ n int }

//sree:imm
type ShortSreeImm struct{ n int }

//gosree:mub p
func sreeMub(p *int) { *p = 1 }

//sree:rob p
func shortSreeRob(p *int) int { return *p }

func sreeLocals() {
	x := 10
	//gosree:mut
	b1 := &x
	b2 := &x //sree:rob
	/*gosree:mut*/ b3 := &x
	b4 := /*sree:rob*/ &x
	//gosree:unsafe testing sree suppression
	_ = b1
	//sree:unsafe testing short sree suppression
	_ = b2
	_ = b3
	_ = b4
}

//gosree:bogus // want `SAFE007: unknown directive //gosree:bogus`
func badSreeDirective() {}
