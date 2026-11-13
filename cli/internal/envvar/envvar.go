// Package envvar reads piste's environment variables: PISTE_<name>.
package envvar

// Prefix is the prefix of every piste environment variable.
const Prefix = "PISTE_"

// Get returns $PISTE_<name>.
func Get(getenv func(string) string, name string) string {
	return getenv(Prefix + name)
}
