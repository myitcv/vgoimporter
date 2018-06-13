package vgoimporter // import "myitcv.io/vgoimporter"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/build"
	"go/importer"
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
	pkgs := make(map[string]*types.Package)
	lookups := make(map[string]io.ReadCloser)

	cmd := exec.Command("vgo", "list", "-test", "-deps", "-export", "-e", "-json", ".")
	cmd.Dir = dir

	// vgo currently does not have a means of silencing resolution output
	// So instead we only read from stdout. Additionally, we can't check
	// the exit code until we have a resolution on https://github.com/golang/go/issues/25842
	// which has made its way into the vgo tree.

	out, _ := cmd.Output()

	// if err != nil {
	// 	if ad, err := filepath.Abs(dir); err == nil {
	// 		dir = ad
	// 	}
	// 	return nil, fmt.Errorf("failed to %v in %v: %v\n%v", strings.Join(cmd.Args, " "), dir, err, string(out))
	// }

	dec := json.NewDecoder(bytes.NewReader(out))

	for {
		var p srcimporter.PkgInfo
		err := dec.Decode(&p)
		if err != nil {
			if io.EOF == err {
				break
			}
			return nil, fmt.Errorf("failed to parse %q in %v: %v\n%s", strings.Join(cmd.Args, " "), dir, err, out)
		}
		if p.Export != "" {
			fi, err := os.Open(p.Export)
			if err != nil {
				return nil, fmt.Errorf("failed to open %v: %v", p.Export, err)
			}
			lookups[p.ImportPath] = fi
		}
		pkgInfos[p.ImportPath] = &p
	}

	lookup := func(path string) (io.ReadCloser, error) {
		rc, ok := lookups[path]
		if !ok {
			return nil, fmt.Errorf("failed to resolve %v", path)
		}

		return rc, nil
	}

	gc := importer.For("gc", lookup)

	for path := range lookups {
		p, err := gc.Import(path)
		if err != nil {
			return nil, fmt.Errorf("failed to gc import %v: %v", path, err)
		}
		pkgs[path] = p
	}

	resolver := func(path string) (*srcimporter.PkgInfo, error) {
		p, ok := pkgInfos[path]
		if !ok {
			return nil, fmt.Errorf("failed to resolve %v amongst (test) -deps in %v", path, dir)
		}

		return p, nil
	}

	return srcimporter.New(resolver, ctxt, fset, pkgs), nil
}
