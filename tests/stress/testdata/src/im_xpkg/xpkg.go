// Package im_xpkg attacks imm types and package variables of another package.
package im_xpkg

import "im_xpkg/sub"

// Promoted exported field of an unexported embedded struct: written from another package.
func promotedField(c *sub.Config) {
	c.Port = 1 // HIT(SAFE005)
}

// Promoted pointer method of the embedded struct, called from another package.
func promotedMethod(c *sub.Config) {
	c.SetPort(2) // HIT(SAFE005)
}

// Another package's exported variable written here: SAFE006 skips globals of other packages,
// and sub's own run sees no write.
func writeOtherGlobal() {
	sub.Global = 1 // MISS(SAFE006): writes package variable sub.Global outside init; globals.go only reports g.Pkg == this package
}

func writeOtherMap() {
	sub.Table["b"] = 2 // MISS(SAFE006): writes sub.Table outside init from another package
}
