package companion_test

import "os"

// osStat is a tiny test helper that returns the size of a
// file at path, or an error. Used by the build tests so they
// don't have to import os themselves.
func osStat(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
