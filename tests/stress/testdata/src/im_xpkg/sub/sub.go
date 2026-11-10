// Package sub declares imm types and exported globals that package im_xpkg attacks.
package sub

// inner is unexported, but its field Port is exported: Go promotes Port through an embedding.
type inner struct{ Port int }

func (i *inner) SetPort(p int) { i.Port = p }

// Config passes the imm shape check (its only field, inner, is unexported and pointer-free), yet
// other packages can name and write Config.Port through the promotion.
//
//gocap:imm
type Config struct { // MISS(SAFE005): Port is promoted from the unexported embedded inner, so other packages and encoding/json can write it; immShapeProblem checks only direct fields for Exported() (witness: im_w_unmarshal)
	inner
}

// New builds a Config.
func New(p int) *Config { return &Config{inner{Port: p}} }

// Global is an exported package variable that this package never writes after initialisation.
var Global int

// Table is an exported map that this package only reads.
var Table = map[string]int{"a": 1}
