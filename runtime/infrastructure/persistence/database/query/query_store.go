package query

// Store is the minimal SQL query dialect contract.
type Store interface {
	Identifier(string) string
	TableIdentifier(string) string
	Placeholder(int) string
}

type storeRenderer struct{ Store }

func (r storeRenderer) Table(value string) string { return r.TableIdentifier(value) }
