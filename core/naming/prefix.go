package naming

// Placement says where an object sits in its bundle, which decides the
// prefixes of its host name (§4.3).
type Placement struct {
	CatalogWords   []string // words of the object's catalog
	NamespaceWords []string // words of the object's namespace
	// MultiCatalog reports whether the bundle has more than one catalog.
	MultiCatalog bool
	// DefaultCatalog reports whether the object's catalog is the one named by
	// default-catalog.
	DefaultCatalog bool
	// DefaultNamespace reports whether the object's namespace is its
	// catalog's default namespace: the dialect's own, or the catalog's
	// default-namespace override.
	DefaultNamespace bool
}

// EntityWords returns an object's final word list: its base words behind the
// prefixes of §4.3 (implementation guide §5.1 entity_words). base is the
// object's name annotation if it has one, else the words of its stored name;
// prefixes apply to annotated names too. The words are never inflected
// (§4.4).
//
// In a bundle of more than one catalog, an object outside the default catalog
// is prefixed with its catalog's words. An object outside its catalog's
// default namespace is prefixed with the namespace's words, whether or not
// its name collides. The catalog prefix comes first.
func EntityWords(base []string, p Placement) []string {
	var out []string
	if p.MultiCatalog && !p.DefaultCatalog {
		out = append(out, p.CatalogWords...)
	}
	if !p.DefaultNamespace {
		out = append(out, p.NamespaceWords...)
	}
	return append(out, base...)
}
