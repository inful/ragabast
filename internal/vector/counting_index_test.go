package vector

import (
	"github.com/blevesearch/bleve/v2"
)

// bleveIdx is an alias of bleve.Index under a different name.
//
// It exists so a struct can EMBED the interface and still satisfy
// it. Embedding bleve.Index directly would give the struct a
// field named "Index", which shadows the promoted Index(id, data)
// method — bleve.Index declares a method with that name — and the
// struct would no longer implement the interface.
//
// Embedding an alias keeps the field name "bleveIdx", so every
// method of bleve.Index is promoted with no collision.
type bleveIdx = bleve.Index

// countingIndex wraps a bleve index and counts the individual
// Delete calls and Batch commits made against it.
//
// bleve.Index declares an Index(id, data) method, so the wrapped
// index cannot be held in an embedded field of the same name — a
// field named Index would shadow the method and break the
// interface. Instead the wrapper embeds a small shim type whose
// ONLY purpose is to forward the interface's methods to the
// wrapped index; the two methods we care about are overridden on
// the shim itself.
type countingIndex struct {
	bleveIdx
	deletes int
	batches int
}

func newCountingIndex(inner bleve.Index) *countingIndex {
	return &countingIndex{bleveIdx: inner}
}

// Delete counts the call before forwarding it.
func (c *countingIndex) Delete(id string) error {
	c.deletes++
	return c.bleveIdx.Delete(id)
}

// Batch counts the commit before forwarding it.
func (c *countingIndex) Batch(b *bleve.Batch) error {
	c.batches++
	return c.bleveIdx.Batch(b)
}
