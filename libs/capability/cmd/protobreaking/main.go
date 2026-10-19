// Command protobreaking fails if the production protos of the capabilities in a checkout break those of a baseline,
// usually main. Production protos are what the SDKs use, so they are only ever added to: a version is changed by
// adding a new production package, never by changing an old one.
//
// A production package can only be retired whole, by adding it to the retired_protos.txt at the root of the
// checkout, one package a line, in the same change that removes it. A retired package stays retired, so it is never
// taken out of the file.
//
// The checks are buf's FILE rules, which keep both the wire format and the code the SDKs generate compatible. The
// protos each module's production protos import are the ones protofetch fetched for it, so both checkouts must
// have been fetched:
//
//	go run ./capability/cmd/protobreaking --against <baseline checkout> [checkout]
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/smartcontractkit/capabilities/libs/capability/cmd/protoc-gen-cre/production"
	"github.com/smartcontractkit/capabilities/libs/capability/protofetch"
)

// retiredFile is the file at the root of a checkout that lists the production packages it retired.
const retiredFile = "retired_protos.txt"

// packageLine is a proto file's package statement.
var packageLine = regexp.MustCompile(`(?m)^package\s+([\w.]+)\s*;`)

// productionRoot is the directory every production proto is in, by its package.
const productionRoot = "capabilities"

func main() {
	against := flag.String("against", "", "checkout of the baseline, usually main")
	flag.Parse()
	head := "."
	if flag.NArg() > 0 {
		head = flag.Arg(0)
	}
	if *against == "" || flag.NArg() > 1 {
		log.Fatal("usage: protobreaking --against <baseline checkout> [checkout]")
	}

	if err := check(context.Background(), *against, head); err != nil {
		log.Fatal(err)
	}
}

func check(ctx context.Context, against, head string) error {
	baseRetired, err := readRetired(against)
	if err != nil {
		return fmt.Errorf("baseline: %w", err)
	}

	retired, err := readRetired(head)
	if err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "protobreaking-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	base, next := filepath.Join(dir, "base"), filepath.Join(dir, "next")
	basePkgs, kept, err := collect(against, base, retired)
	if err != nil {
		return fmt.Errorf("baseline: %w", err)
	}

	nextPkgs, _, err := collect(head, next, nil)
	if err != nil {
		return err
	}

	if err = checkRetired(basePkgs, nextPkgs, baseRetired, retired); err != nil {
		return err
	}

	// What's left of the baseline once its retired packages are taken out is what can break.
	if kept == 0 {
		return nil
	}

	return breaking(ctx, dir)
}

// readRetired reads the packages the retired file at root lists, ignoring blank lines and # comments. A checkout
// without one has retired nothing.
func readRetired(root string) (map[string]bool, error) {
	b, err := os.ReadFile(filepath.Join(root, retiredFile)) //nolint:gosec // A file in the checkout.
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}

	if err != nil {
		return nil, err
	}

	retired := map[string]bool{}
	for line := range strings.Lines(string(b)) {
		pkg, _, _ := strings.Cut(line, "#")
		if pkg = strings.TrimSpace(pkg); pkg != "" {
			retired[pkg] = true
		}
	}

	return retired, nil
}

func checkRetired(base, next, baseRetired, retired map[string]bool) error {
	var errs []error
	for _, pkg := range sorted(base) {
		if !next[pkg] && !retired[pkg] {
			errs = append(errs, fmt.Errorf("production package %s was removed: add it to %s to retire it", pkg, retiredFile))
		}
	}

	for _, pkg := range sorted(retired) {
		if next[pkg] {
			errs = append(errs, fmt.Errorf("production package %s is retired but still exists: a package can only be retired whole", pkg))
		}
	}

	for _, pkg := range sorted(baseRetired) {
		if !retired[pkg] {
			errs = append(errs, fmt.Errorf("retired production package %s was taken out of %s: a retired package stays retired", pkg, retiredFile))
		}
	}

	return errors.Join(errs...)
}

func sorted(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// collect copies the production protos of every capability in the checkout at root to out, at their paths, with
// the protos protofetch fetched for each, and returns their packages and how many files it copied. Packages in skip
// are left out, so they are not compared, but are still returned.
func collect(root, out string, skip map[string]bool) (map[string]bool, int, error) {
	pkgs := map[string]bool{}
	from := map[string]string{}
	copied := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.Name() != production.Dir || filepath.Base(filepath.Dir(path)) != "protos" {
			return nil
		}
		module, err := moduleOf(root, path)
		if err != nil {
			return err
		}
		n, err := copyProduction(path, out, pkgs, from, skip)
		if err != nil {
			return err
		}
		copied += n
		return copyFetched(module, out)
	})
	return pkgs, copied, err
}

// moduleOf returns the directory of the module dir is in, under root: the nearest with a go.mod.
func moduleOf(root, dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		if d == root || d == filepath.Dir(d) {
			return "", fmt.Errorf("%s is not in a module", dir)
		}
	}
}

// copyProduction copies the production protos in dir to out, at their paths, adding their packages to pkgs and
// noting where each came from, in from, as two capabilities can't have the same production proto. It returns how
// many it copied.
func copyProduction(dir, out string, pkgs map[string]bool, from map[string]string, skip map[string]bool) (int, error) {
	copied := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".proto" {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if other, ok := from[rel]; ok {
			return fmt.Errorf("%s is the production proto of both %s and %s", filepath.ToSlash(rel), other, dir)
		}
		from[rel] = dir

		content, err := os.ReadFile(path) //nolint:gosec // A proto in the checkout.
		if err != nil {
			return err
		}
		m := packageLine.FindSubmatch(content)
		if m == nil {
			return fmt.Errorf("%s has no package", path)
		}
		pkg := string(m[1])
		pkgs[pkg] = true
		if skip[pkg] {
			return nil
		}
		copied++
		return write(filepath.Join(out, rel), content)
	})
	return copied, err
}

// copyFetched copies the protos protofetch fetched for the module at dir to out, at their paths, for the production
// protos to import. Modules share the protofetch.toml at the root of the repo, so they fetch the same base protos.
func copyFetched(dir, out string) error {
	root, err := protofetch.Root(dir)
	if err != nil {
		return fmt.Errorf("%s has production protos, but: %w", dir, err)
	}
	fetched, err := protofetch.OutDir(root)
	if err != nil {
		return err
	}
	if _, err = os.Stat(fetched); err != nil {
		return fmt.Errorf("the protos %s imports are not fetched, run protofetch fetch --locked: %w", dir, err)
	}

	return filepath.WalkDir(fetched, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".proto" {
			return err
		}
		rel, err := filepath.Rel(fetched, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(rel), productionRoot+"/") {
			return fmt.Errorf("%s fetches %s, which is where production protos are", dir, filepath.ToSlash(rel))
		}
		content, err := os.ReadFile(path) //nolint:gosec // Fetched by protofetch.
		if err != nil {
			return err
		}
		return write(filepath.Join(out, rel), content)
	})
}

func write(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o600) //nolint:gosec // Under the temporary directory, at a path relative to it.
}

// breaking runs buf breaking on the protos collected in dir/next against those in dir/base. Each is built to an
// image of the production protos alone, which marks what they import as imports, so only production protos are
// checked.
func breaking(ctx context.Context, dir string) error {
	for _, side := range []string{"base", "next"} {
		cmd := exec.CommandContext(ctx, "buf", "build", "--path", productionRoot, "-o", filepath.Join("..", side+".binpb")) //nolint:gosec // Constant arguments.
		cmd.Dir = filepath.Join(dir, side)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to build the %s production protos: %w\n%s", side, err, out)
		}
	}

	cmd := exec.CommandContext(ctx, "buf", "breaking", "next.binpb", "--against", "base.binpb", "--exclude-imports",
		"--config", `{"version":"v2","breaking":{"use":["FILE"]}}`, "--error-format", "json")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	return fmt.Errorf("production protos have breaking changes:\n%s", findings(strings.NewReader(string(out))))
}

// findings formats buf's JSON findings, one per line, with the rule each breaks.
func findings(out io.Reader) string {
	var sb strings.Builder
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		var f struct {
			Path, Type, Message string
			StartLine           int `json:"start_line"`
		}
		if err := json.Unmarshal(sc.Bytes(), &f); err != nil || f.Type == "" {
			sb.WriteString("\t" + sc.Text() + "\n")
			continue
		}
		fmt.Fprintf(&sb, "\t%s:%d: %s (%s)\n", f.Path, f.StartLine, f.Message, f.Type)
	}
	return sb.String()
}
