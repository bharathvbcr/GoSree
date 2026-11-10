// Package sub owns a package variable it never writes after its declaration. Its own analysis has
// nothing to report: the write is in the importer.
package sub

var Limit = 1

func Read() int { return Limit }
