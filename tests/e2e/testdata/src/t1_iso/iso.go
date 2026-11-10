package t1_iso

type packet struct {
	id   int
	data string
}

// TC-T1-5.1: \iso pointer sent on channel: subsequent read is use-after-move (SAFE003)
func channelSendReadUse(ch chan *packet) string {
	p := &packet{id: 1, data: "hello"}
	ch <- p
	return p.data // want `SAFE003: composite literal is used after it was sent on a channel`
}

// TC-T1-5.2: \iso pointer sent on channel: subsequent write is use-after-move (SAFE003)
func channelSendWriteUse(ch chan *packet) {
	p := &packet{id: 1, data: "hello"}
	ch <- p
	p.data = "world" // want `SAFE003: composite literal is used after it was sent on a channel`
}

// TC-T1-5.3: \iso pointer captured by goroutine closure: subsequent use is use-after-move (SAFE003)
func goroutineCaptureUse() int {
	p := &packet{id: 1, data: "async"}
	go func() {
		p.id = 2
	}()
	p.id = 3 // want `SAFE003: variable p is used after it was captured by a goroutine`
	return p.id
}

// TC-T1-5.4: \iso pointer passed to owned parameter: caller binding invalidated (SAFE003)
//
//gosree:iso p
func consumePacket(ch chan *packet, p *packet) {
	ch <- p
}

func paramMoveUse(ch chan *packet) int {
	p := &packet{id: 1, data: "owned"}
	consumePacket(ch, p)
	return p.id // want `SAFE003: composite literal is used after it was passed to owned parameter 1`
}

// TC-T1-5.5: Normal unshared local value never moved (Clean Pass)
func unsharedLocal() int {
	p := &packet{id: 42, data: "unshared"}
	p.id++
	return p.id
}
