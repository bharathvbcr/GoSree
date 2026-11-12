package ownership

import "sync"

var releaseObserverMu sync.Mutex

func ObserveRelease(f func(pkgPath string, work, stepCaps, countCaps int)) (stop func()) {
	releaseObserverMu.Lock()
	releaseObserver = f
	releaseObserverMu.Unlock()
	return func() {
		releaseObserverMu.Lock()
		releaseObserver = nil
		releaseObserverMu.Unlock()
	}
}
