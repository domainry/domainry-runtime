package query

// Store is the minimal SQL query dialect contract.
type Store interface {
	Identifier(string) string
	TableIdentifier(string) string
	Placeholder(int) string
}
