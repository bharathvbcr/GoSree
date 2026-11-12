// WITNESS: race
//
// A handler writes a dependency every request shares, reached through a getter instead of a field
// read. Found on the reference service: its handler called gateway.ResearchLoop().Run(...), the same
// call through the field was reported, and through the getter it was not.
//
// SAFE008 ties an argument back to the handler's shared memory with sharedRoot
// (passes/ownership/handlerwrite.go). At a call result that walk continues only into a callee whose
// body is in this package and has one result. The getter's exported summary links its result to its
// receiver, and the walk does not read it.
//
// main requests only /locked, so the race the runtime reports is the one behind the MISS there.
package main

import (
	"net/http"
	"net/http/httptest"
	"sync"

	"lk_w_getter/dep"
)

type handler struct{ gw *dep.Gateway }

// Control: the dependency read as a field.
func (h *handler) viaField(http.ResponseWriter, *http.Request) {
	l := h.gw.Loop
	l.Run() // HIT(SAFE008)
}

// The same pointer through a method of another package.
func (h *handler) viaPlainGetter(http.ResponseWriter, *http.Request) {
	l := h.gw.Plain()
	l.Run() // HIT(SAFE008): the summary of Plain links its result to its receiver
}

// The getter takes a lock around the pointer read. The lock is not what hides the write: Plain
// hides it too.
func (h *handler) viaLockedGetter(w http.ResponseWriter, _ *http.Request) {
	l := h.gw.Locked()
	l.Run() // HIT(SAFE008): the runtime convicts this one
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) loop() *dep.Loop { return h.gw.Loop }

// Control: a getter of this package with one result. The walk follows its body.
func (h *handler) viaLocalGetter(http.ResponseWriter, *http.Request) {
	l := h.loop()
	l.Run() // HIT(SAFE008)
}

func (h *handler) lookup() (*dep.Loop, bool) { return h.gw.Loop, h.gw.Loop != nil }

// A getter of this package with two results.
func (h *handler) viaTupleGetter(http.ResponseWriter, *http.Request) {
	l, ok := h.lookup()
	if !ok {
		return
	}
	l.Run() // HIT(SAFE008): each return is followed at the result index
}

// The (value, ok) getter in the other package.
func (h *handler) viaRemoteTuple(http.ResponseWriter, *http.Request) {
	l, ok := h.gw.Lookup()
	if !ok {
		return
	}
	l.Run() // HIT(SAFE008)
}

// Pair returns (fresh, shared). Writing the first result is not a finding.
func (h *handler) viaPairFresh(http.ResponseWriter, *http.Request) {
	fresh, _ := h.gw.Pair()
	fresh.Run()
}

// Writing the second result is: the result index matters.
func (h *handler) viaPairShared(http.ResponseWriter, *http.Request) {
	_, shared := h.gw.Pair()
	shared.Run() // HIT(SAFE008): the second result is the shared one
}

// A getter on the result of a getter.
func (h *handler) viaChain(http.ResponseWriter, *http.Request) {
	l := h.gw.Self().Plain()
	l.Run() // HIT(SAFE008)
}

// Not a finding: a loop made for this request.
func (h *handler) viaFresh(http.ResponseWriter, *http.Request) {
	l := dep.NewLoop()
	l.Run()
}

// Not a finding: Boxed returns a new Box, and Touch writes only that.
func (h *handler) viaBox(http.ResponseWriter, *http.Request) {
	b := h.gw.Boxed()
	b.Touch()
}

// The new Box holds the shared loop.
func (h *handler) viaBoxContents(http.ResponseWriter, *http.Request) {
	b := h.gw.Boxed()
	b.L.Run() // MISS(SAFE008): the new Box holds the gateway's loop; a result that only contains shared memory is not followed back to it
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/field", (&handler{gw: dep.New()}).viaField)
	mux.HandleFunc("/plain", (&handler{gw: dep.New()}).viaPlainGetter)
	mux.HandleFunc("/locked", (&handler{gw: dep.New()}).viaLockedGetter)
	mux.HandleFunc("/local", (&handler{gw: dep.New()}).viaLocalGetter)
	mux.HandleFunc("/tuple", (&handler{gw: dep.New()}).viaTupleGetter)
	mux.HandleFunc("/remote-tuple", (&handler{gw: dep.New()}).viaRemoteTuple)
	mux.HandleFunc("/pair-fresh", (&handler{gw: dep.New()}).viaPairFresh)
	mux.HandleFunc("/pair-shared", (&handler{gw: dep.New()}).viaPairShared)
	mux.HandleFunc("/chain", (&handler{gw: dep.New()}).viaChain)
	mux.HandleFunc("/fresh", (&handler{gw: dep.New()}).viaFresh)
	mux.HandleFunc("/box", (&handler{gw: dep.New()}).viaBox)
	mux.HandleFunc("/box-contents", (&handler{gw: dep.New()}).viaBoxContents)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	url := srv.URL + "/locked"
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				resp, err := http.Get(url)
				if err != nil {
					return
				}
				_ = resp.Body.Close()
			}
		}()
	}
	wg.Wait()
}
