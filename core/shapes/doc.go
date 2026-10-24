// Package shapes computes the shape layer from a catalog IR that the facts
// pass has completed (design record §14): every entity's presets in their
// forms, relation-qualified child shapes, custom shapes and upsert
// exclusions, fully expanded so emitters never re-implement the algebra
// (§14.10).
package shapes
