package srcimporter // import "myitcv.io/vgoimporter/srcimporter"

// This package borrows heavily from go/internal/srcimporter; with thanks to gri et all

// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sync"

	"myitcv.io/vgoimporter/gcimporter"
)

type PkgInfo struct {
	ImportPath  string
	Dir         string
	Export      string
	Standard    bool
	Name        string
	GoFiles     []string
	CgoFiles    []string
	TestImports []string
}

type Resolver func(string) (*PkgInfo, error)

type Importer struct {
	ctxt     *build.Context
	resolver Resolver
	fset     *token.FileSet
	sizes    types.Sizes
	packages map[string]*types.Package
}

func New(resolver Resolver, ctxt *build.Context, fset *token.FileSet, packages map[string]*types.Package) *Importer {
	return &Importer{
		resolver: resolver,
		ctxt:     ctxt,
		fset:     fset,
		sizes:    types.SizesFor(ctxt.Compiler, ctxt.GOARCH), // uses go/types default if GOARCH not found
		packages: packages,
	}
}

// Importing is a sentinel taking the place in Importer.packages
// for a package that is in the process of being imported.
var importing types.Package

// Import(path) is a shortcut for ImportFrom(path, ".", 0).
func (p *Importer) Import(path string) (*types.Package, error) {
	return p.ImportFrom(path, ".", 0) // use "." rather than "" (see issue #24441)
}

// ImportFrom imports the package with the given import path resolved from the given srcDir,
// adds the new package to the set of packages maintained by the importer, and returns the
// package. Package path resolution and file system operations are controlled by the context
// maintained with the importer. The import mode must be zero but is otherwise ignored.
// Packages that are not comprised entirely of pure Go files may fail to import because the
// type checker may not be able to determine all exported entities (e.g. due to cgo dependencies).
func (p *Importer) ImportFrom(path, srcDir string, mode types.ImportMode) (*types.Package, error) {
	if mode != 0 {
		panic("non-zero import mode")
	}

	if path == "unsafe" {
		return types.Unsafe, nil
	}

	if abs, err := p.absPath(srcDir); err == nil { // see issue #14282
		srcDir = abs
	}
	pkgInfo, err := p.resolver(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %v: %v", path, err)
	}

	if pkgInfo.Standard {
		gcimports := &gcimports{
			packages: p.packages,
		}
		return gcimports.ImportFrom(path, srcDir, mode)
	}

	// no need to re-import if the package was imported completely before
	pkg := p.packages[pkgInfo.ImportPath]
	if pkg != nil {
		if pkg == &importing {
			return nil, fmt.Errorf("import cycle through package %q", pkgInfo.ImportPath)
		}
		if !pkg.Complete() {
			// Package exists but is not complete - we cannot handle this
			// at the moment since the source importer replaces the package
			// wholesale rather than augmenting it (see #19337 for details).
			// Return incomplete package with error (see #16088).
			return pkg, fmt.Errorf("reimported partially imported package %q", pkgInfo.ImportPath)
		}
		return pkg, nil
	}

	p.packages[pkgInfo.ImportPath] = &importing
	defer func() {
		// clean up in case of error
		// TODO(gri) Eventually we may want to leave a (possibly empty)
		// package in the map in all cases (and use that package to
		// identify cycles). See also issue 16088.
		if p.packages[pkgInfo.ImportPath] == &importing {
			p.packages[pkgInfo.ImportPath] = nil
		}
	}()

	var filenames []string
	filenames = append(filenames, pkgInfo.GoFiles...)
	filenames = append(filenames, pkgInfo.CgoFiles...)

	files, err := p.parseFiles(pkgInfo.Dir, filenames)
	if err != nil {
		return nil, err
	}

	// type-check package files
	var firstHardErr error
	conf := types.Config{
		IgnoreFuncBodies: true,
		FakeImportC:      true,
		// continue type-checking after the first error
		Error: func(err error) {
			if firstHardErr == nil && !err.(types.Error).Soft {
				firstHardErr = err
			}
		},
		Importer: p,
		Sizes:    p.sizes,
	}
	pkg, err = conf.Check(pkgInfo.ImportPath, p.fset, files, nil)
	if err != nil {
		// If there was a hard error it is possibly unsafe
		// to use the package as it may not be fully populated.
		// Do not return it (see also #20837, #20855).
		if firstHardErr != nil {
			pkg = nil
			err = firstHardErr // give preference to first hard error over any soft error
		}
		return pkg, fmt.Errorf("type-checking package %q failed (%v)", pkgInfo.ImportPath, err)
	}
	if firstHardErr != nil {
		// this can only happen if we have a bug in go/types
		panic("package is not safe yet no error was returned")
	}

	p.packages[pkgInfo.ImportPath] = pkg
	return pkg, nil
}

func (p *Importer) parseFiles(dir string, filenames []string) ([]*ast.File, error) {
	// use build.Context's OpenFile if there is one
	open := p.ctxt.OpenFile
	if open == nil {
		open = func(name string) (io.ReadCloser, error) { return os.Open(name) }
	}

	files := make([]*ast.File, len(filenames))
	errors := make([]error, len(filenames))

	var wg sync.WaitGroup
	wg.Add(len(filenames))
	for i, filename := range filenames {
		go func(i int, filepath string) {
			defer wg.Done()
			src, err := open(filepath)
			if err != nil {
				errors[i] = err // open provides operation and filename in error
				return
			}
			files[i], errors[i] = parser.ParseFile(p.fset, filepath, src, 0)
			src.Close() // ignore Close error - parsing may have succeeded which is all we need
		}(i, p.joinPath(dir, filename))
	}
	wg.Wait()

	// if there are errors, return the first one for deterministic results
	for _, err := range errors {
		if err != nil {
			return nil, err
		}
	}

	return files, nil
}

// context-controlled file system operations

func (p *Importer) absPath(path string) (string, error) {
	// TODO(gri) This should be using p.ctxt.AbsPath which doesn't
	// exist but probably should. See also issue #14282.
	return filepath.Abs(path)
}

func (p *Importer) isAbsPath(path string) bool {
	if f := p.ctxt.IsAbsPath; f != nil {
		return f(path)
	}
	return filepath.IsAbs(path)
}

func (p *Importer) joinPath(elem ...string) string {
	if f := p.ctxt.JoinPath; f != nil {
		return f(elem...)
	}
	return filepath.Join(elem...)
}

type gcimports struct {
	packages map[string]*types.Package
}

func (m *gcimports) Import(path string) (*types.Package, error) {
	return m.ImportFrom(path, "" /* no vendoring */, 0)
}

func (m *gcimports) ImportFrom(path, srcDir string, mode types.ImportMode) (*types.Package, error) {
	if mode != 0 {
		panic("mode must be 0")
	}
	return gcimporter.Import(m.packages, path, srcDir, nil)
}
