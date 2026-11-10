// WITNESS: race
//
// The most common production handler shape: a factory returns the handler closure and the caller
// registers the call's result. handlerRoots does not resolve a call result, so the closure's write
// to its captured counter is never checked, and two concurrent requests race on it.
package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
)

func countingHandler() http.HandlerFunc {
	hits := 0
	return func(w http.ResponseWriter, _ *http.Request) {
		hits++ // MISS(SAFE008): every request runs this closure and shares hits
		w.WriteHeader(http.StatusNoContent)
	}
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", countingHandler())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	url := srv.URL
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
