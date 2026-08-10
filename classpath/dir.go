package classpath

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// dirEntry is a directory of class files laid out by package.
type dirEntry struct {
	root string

	once  sync.Once
	names []string
	err   error
}

func openDir(root string) *dirEntry { return &dirEntry{root: root} }

func (d *dirEntry) Kind() Kind        { return KindDir }
func (d *dirEntry) Container() string { return d.root }

func (d *dirEntry) Class(binary string) (*Class, error) {
	if !ValidBinaryName(binary) {
		return nil, &NotFoundError{Binary: binary, Container: d.root}
	}
	rel := entryName(binary)
	origin := Origin{Kind: KindDir, Container: d.root, Entry: rel}

	data, err := os.ReadFile(filepath.Join(d.root, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &NotFoundError{Binary: binary, Container: d.root}
		}
		return nil, &Error{Origin: origin, Err: err}
	}
	return &Class{Binary: binary, Data: data, Origin: origin}, nil
}

// Names walks the tree once and caches the result. A directory that changes
// underneath an open Path is out of scope: a build reads its inputs once.
func (d *dirEntry) Names() ([]string, error) {
	d.once.Do(func() {
		err := filepath.WalkDir(d.root, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".class") {
				return nil
			}
			rel, err := filepath.Rel(d.root, p)
			if err != nil {
				return err
			}
			if b, ok := binaryName(filepath.ToSlash(rel)); ok {
				d.names = append(d.names, b)
			}
			return nil
		})
		d.err = err
		sort.Strings(d.names)
	})
	return d.names, d.err
}

func (d *dirEntry) Close() error { return nil }