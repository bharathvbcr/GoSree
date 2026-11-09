package t4_scenarios

import "sync"

// =========================================================================
// Scenario 1: Realistic Concurrent Worker Pool with \iso Work Items and Fork-Join Sync
// =========================================================================

type Job struct {
	ID     int
	Result int
}

//gocap:mut j
func ProcessJob(j *Job) {
	j.Result = j.ID * 10
}

func WorkerPoolScenario() []int {
	jobs := []*Job{
		{ID: 1},
		{ID: 2},
		{ID: 3},
	}
	var wg sync.WaitGroup
	for _, j := range jobs {
		job := j
		wg.Add(1)
		go func() {
			defer wg.Done()
			ProcessJob(job)
		}()
	}
	wg.Wait()
	// Master accesses results cleanly after wg.Wait()
	results := make([]int, len(jobs))
	for i, j := range jobs {
		results[i] = j.Result
	}
	return results
}

// =========================================================================
// Scenario 2: Multi-Stage Bounded Processing Pipeline with Channel Passing
// =========================================================================

type PipelineBuffer struct {
	Stage int
	Data  []byte
}

func Stage1Producer(out chan *PipelineBuffer) {
	buf := &PipelineBuffer{Stage: 1, Data: []byte("raw_data")}
	out <- buf // Moved to Stage 2
}

func Stage2Transformer(in chan *PipelineBuffer, out chan *PipelineBuffer) {
	buf := <-in
	buf.Stage = 2
	out <- buf // Moved to Stage 3
}

func Stage3Consumer(in chan *PipelineBuffer) int {
	buf := <-in
	buf.Stage = 3
	return len(buf.Data)
}

func RunPipeline() int {
	ch1 := make(chan *PipelineBuffer, 1)
	ch2 := make(chan *PipelineBuffer, 1)
	Stage1Producer(ch1)
	Stage2Transformer(ch1, ch2)
	return Stage3Consumer(ch2)
}

// =========================================================================
// Scenario 3: Real-Time Event Processor with Shared \imm Config & Second-Class Borrows
// =========================================================================

//gocap:imm
type ProcessorConfig struct {
	MaxBatchSize int
	WorkerCount  int
}

type EventBatch struct {
	Events []int
}

//gocap:rob cfg
//gocap:mut batch
func ProcessEventBatch(cfg *ProcessorConfig, batch *EventBatch) int {
	sum := 0
	for i := 0; i < len(batch.Events) && i < cfg.MaxBatchSize; i++ {
		batch.Events[i] *= 2
		sum += batch.Events[i]
	}
	return sum
}

func EventProcessorScenario() int {
	cfg := &ProcessorConfig{MaxBatchSize: 100, WorkerCount: 4}
	batch := &EventBatch{Events: []int{1, 2, 3, 4, 5}}
	return ProcessEventBatch(cfg, batch)
}

// =========================================================================
// Scenario 4: In-Memory Key-Value Cache with Partitioned Read/Write Borrows
// =========================================================================

type CacheEntry struct {
	Value string
}

type CachePartition struct {
	Entries map[string]CacheEntry
}

//gocap:mut cp
func (cp *CachePartition) Put(k string, v string) {
	cp.Entries[k] = CacheEntry{Value: v}
}

//gocap:rob cp
func (cp *CachePartition) Get(k string) (string, bool) {
	entry, ok := cp.Entries[k]
	return entry.Value, ok
}

func CacheScenario() (string, bool) {
	cp := CachePartition{Entries: make(map[string]CacheEntry)}
	cp.Put("alpha", "100") // Exclusive mutable borrow
	return cp.Get("alpha") // Read-only borrow after mut ends
}

// =========================================================================
// Scenario 5: Adversarial Multi-Goroutine Penetration Suite (Negative Attacks)
// =========================================================================

// Attack 1: Double move of \iso pointer to two independent goroutines
func Attack1DoubleMove() {
	j := &Job{ID: 1}
	go func() {
		_ = j.ID
	}()
	go func() { // want `SAFE003: composite literal is used after it was captured by a goroutine`
		_ = j.ID
	}()
}

// Attack 2: Channel send followed by read
func Attack2UseAfterChannelSend(ch chan *Job) int {
	j := &Job{ID: 2}
	ch <- j
	return j.ID // want `SAFE003: composite literal is used after it was sent on a channel`
}

// Attack 3: Channel send followed by write
func Attack3WriteAfterChannelSend(ch chan *Job) {
	j := &Job{ID: 3}
	ch <- j
	j.ID = 99 // want `SAFE003: composite literal is used after it was sent on a channel`
}

// Attack 4: Goroutine capture followed by parent write
func Attack4ParentWriteAfterCapture() {
	x := 0
	go func() {
		x = 1
	}()
	x = 2 // want `SAFE003: variable x is used after it was captured by a goroutine`
}

// Attack 5: Stack borrow escape over channel
func Attack5StackBorrowChannelEscape(ch chan *int) {
	val := 10
	//gocap:mut
	b := &val
	ch <- b // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary`
}

// Attack 6: Stack borrow capture in goroutine closure
func Attack6StackBorrowGoroutineEscape() {
	val := 10
	//gocap:mut
	b := &val
	go func() { // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary`
		*b = 20
	}()
}

// Attack 7: Second-class borrow in struct field
type Attack7Struct struct {
	//gocap:mut
	P *int // want `BORROW001: struct field 'P' cannot have second-class borrow type`
}

// Attack 8: Borrow stored into package global
var Attack8Global *int

func Attack8GlobalStore() {
	x := 10
	//gocap:mut
	b := &x
	Attack8Global = b // want `BORROW001: package variable 'Attack8Global' cannot store second-class borrow`
}

// Attack 9: Dangling return of stack reference
func Attack9DanglingStackReturn() *int {
	x := 10
	//gocap:rob
	return &x // want `BORROW004: cannot return reference to local stack variable 'x'`
}

// Attack 10: Mutation of field on immutable type
func Attack10MutationOfImm() {
	cfg := &ProcessorConfig{MaxBatchSize: 10, WorkerCount: 2}
	cfg.MaxBatchSize = 20 // want `SAFE005: write to a field of ProcessorConfig, which is //gocap:imm`
}
