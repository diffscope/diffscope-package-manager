package packagearchive

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Directory confines declaration file access to root, including through symlinks.
type Directory struct{ root *os.Root }

func OpenDirectory(name string) (*Directory, error) {
	r, err := os.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	return &Directory{r}, nil
}
func (d *Directory) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	return d.root.Open(filepath.FromSlash(name))
}
func (d *Directory) Close() error { return d.root.Close() }
