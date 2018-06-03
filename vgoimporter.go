package vgoimporter // import "myitcv.io/vgoimporter"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/build"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"myitcv.io/hybridimporter"
	"myitcv.io/vgoimporter/srcimporter"
)

// New returns a go/types.ImporterFrom that uses vgo list to deduce source
// file locations for non-standard library packages, and a gcimporter for
// standard library packages. This is hopelessly inefficient for a couple of
// reasons:
//
// 1. vgo list currently doesn't understand -test, hence we need two invocations
//    to get the details of the test binary
// 2. vgo list currently doesn't understand -build, hence we have to use a
//    source-file based type checker.
// 3. vgo list currently doesn't have a -nodownload option, hence the output from
//    it can be polluted with lines like:
//
//    vgo: finding rsc.io/quote v1.5.2
//
//    Hence for now we skip until we see a '{' or EOF
//
func New(ctxt *build.Context, fset *token.FileSet, dir string) (types.ImporterFrom, error) {
	_, ok := os.LookupEnv("VGO_CONTEXT")
	if ok {
		return nil, fmt.Errorf("dont't yet know how to handle VGO_CONTEXT")
	}

	// first try to find a go.mod upwards
	found := false
	searchDir := dir
	for {
		fi, err := os.Stat(filepath.Join(searchDir, "go.mod"))
		if err == nil && !fi.IsDir() {
			found = true
			break
		}
		pdir := filepath.Dir(searchDir)
		if pdir == searchDir {
			break
		}
		searchDir = pdir
	}

	if !found {
		return hybridimporter.New(ctxt, fset, dir)
	}

	// we know dir is within a Go module

	pkgInfos := make(map[string]*srcimporter.PkgInfo)

	load := func(paths ...string) (*srcimporter.PkgInfo, error) {
		args := append([]string{"vgo", "list", "-deps", "-json"}, paths...)
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir

		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("failed to start %q in %v: %v\n%s", strings.Join(cmd.Args, " "), dir, err, out)
		}

		for {
			if out[0] == '{' {
				break
			}

			i := bytes.Index(out, []byte{'\n'})
			if i == -1 {
				out = nil
				break
			}
			out = out[i+1:]
		}

		dec := json.NewDecoder(bytes.NewReader(out))
		var last *srcimporter.PkgInfo

		for {
			var p srcimporter.PkgInfo
			err := dec.Decode(&p)
			if err != nil {
				if io.EOF == err {
					break
				}
				return nil, fmt.Errorf("failed to parse %q in %v: %v\n%s", strings.Join(cmd.Args, " "), dir, err, out)
			}
			pkgInfos[p.ImportPath] = &p
			last = &p
		}

		return last, nil
	}

	self, err := load(".")
	if err != nil {
		return nil, fmt.Errorf("failed to list -deps in %v: %v", dir, err)
	}
	if self == nil || self.Dir != dir {
		return nil, fmt.Errorf("failed to properly resolve self; got %#v", self)
	}
	if _, err := load(self.TestImports...); err != nil {
		return nil, fmt.Errorf("failed to list -deps %v in %v: %v", strings.Join(self.TestImports, " "), dir, err)
	}

	resolver := func(path string) (*srcimporter.PkgInfo, error) {
		p, ok := pkgInfos[path]
		if !ok {
			return nil, fmt.Errorf("failed to resolve %v amongst (test) -deps in %v", path, dir)
		}

		return p, nil
	}

	pkgs := make(map[string]*types.Package)

	return srcimporter.New(resolver, ctxt, fset, pkgs), nil
}
