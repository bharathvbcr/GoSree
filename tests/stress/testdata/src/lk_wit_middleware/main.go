// WITNESS: race
//
// A handler struct registered through a standard decorator (http.StripPrefix). The registered value
// is the decorator's result, so ServeHTTP's receiver write is never checked, and concurrent
// requests race on it. RULES.md (SAFE008) covers any net/http.Handler.
package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
)

type Stats struct{ served int }

func (s *Stats) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.served++ // MISS(SAFE008): every request shares the receiver
	w.WriteHeader(http.StatusNoContent)
}

func main() {
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", &Stats{}))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	url := srv.URL + "/api/x"
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
