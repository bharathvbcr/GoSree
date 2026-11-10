// Package im_container attacks SAFE005 by writing the memory that holds an imm value rather than
// the imm value itself: a *Config handed out earlier (&w.cfg, &cs[i], &a[i]) sees the new bytes.
package im_container

// Config is a valid imm type.
//
//gosree:imm
type Config struct {
	port int
}

// Wrapper is an ordinary struct that holds an imm value inline.
type Wrapper struct {
	cfg  Config
	note int
}

// FP probe: building a container whose field is an imm literal is construction, not a write.
func buildWrapper() *Wrapper {
	return &Wrapper{cfg: Config{port: 1}} // FP(SAFE005): the Config is written by its own composite literal nested in the Wrapper literal; nothing exists to share yet
}

// Control: overwriting the inline imm field is caught.
func overwriteField(w *Wrapper) {
	w.cfg = Config{port: 2} // HIT(SAFE005)
}

// Attack: overwriting the whole container rewrites w.cfg, which may already be shared as &w.cfg.
func overwriteContainer(w *Wrapper) {
	*w = Wrapper{note: 2} // HIT(SAFE005): the store is through a *Wrapper and rewrites the inline imm field w.cfg
}

// Control: assigning an element of a slice of imm values is caught.
func assignElem(cs []Config) {
	cs[0] = Config{port: 1} // HIT(SAFE005)
}

// Control: the builtin copy into a []Config is caught.
func copyElems(cs []Config) {
	copy(cs, []Config{{port: 1}}) // HIT(SAFE005)
}

// Control: append over existing capacity is caught.
func appendInPlace(cs []Config) []Config {
	return append(cs[:0], Config{port: 1}) // HIT(SAFE005)
}

// Attack: clear zeroes the elements in place.
func clearElems(cs []Config) {
	clear(cs) // HIT(SAFE005): clear zeroes every Config in cs, any of which may be shared as &cs[i]
}

// Attack: an array of imm values overwritten as a whole through its pointer (witness: im_w_array).
func overwriteArray(a *[2]Config) {
	*a = [2]Config{} // HIT(SAFE005): *a's store rewrites a[0] and a[1]
}

// Control: assigning one array element through the pointer is caught.
func assignArrayElem(a *[2]Config) {
	a[0] = Config{} // HIT(SAFE005)
}
