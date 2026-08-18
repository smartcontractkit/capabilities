package generator

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	protoVendor = ".proto_vendor"
	dirPerm     = 0700
)

// pseudoVersion matches the "-<timestamp>-<commit>" suffix every Go
// pseudo-version ends with. The capture group is the commit to check out.
var pseudoVersion = regexp.MustCompile(`-[0-9]{14}-([0-9a-f]{12})$`)

// downloader vendors the .proto sources of Go module dependencies.
//
// protoc needs every transitive import on disk, but a module such as
// github.com/smartcontractkit/chainlink-protos/cre/go only publishes generated
// Go code: the .proto files it was generated from live above the module root in
// the repository, so they never reach the module cache. download resolves each
// requested module against the caller's go.mod, copies the requested paths into
// .proto_vendor, and returns the directories to hand protoc as -I.
type downloader struct {
	// Items maps a Go module path to the paths, relative to that module's root,
	// that hold the .proto files to vendor. A path may point above the module
	// root (for example "../") for modules whose .proto files sit outside the
	// Go module; doing so forces a clone, since the module cache only holds the
	// module's own subtree.
	Items map[string][]string

	// Dir is the directory whose go.mod declares the modules in Items. Empty
	// means the current working directory.
	Dir string

	// Vendor is the directory the .proto_vendor cache is kept in, defaulting to
	// Dir. Copies are keyed by module and version, so pointing several modules
	// at one directory - the root of the repository they share, say - fetches a
	// given release of a dependency once rather than once per module.
	Vendor string
}

func (d *downloader) vendorDir() string {
	if d.Vendor == "" {
		return filepath.Join(d.Dir, protoVendor)
	}
	return filepath.Join(d.Vendor, protoVendor)
}

// vendored is one configured path of one module, after vendoring.
type vendored struct {
	// Module is the module path the path was configured under.
	Module string

	// Path is the path as configured, relative to the module root.
	Path string

	// Dir is where the vendored copy of Path is on disk. It is a file rather
	// than a directory when Path named a single .proto.
	Dir string

	// Include is the directory protoc has to be given as -I for imports
	// written against Path to resolve. Several paths of one module commonly
	// share it; see Includes.
	Include string
}

// download vendors every configured module and returns one entry per requested
// path, in the order the paths were configured.
func (d *downloader) download() ([]vendored, error) {
	if len(d.Items) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(d.vendorDir(), dirPerm); err != nil {
		return nil, err
	}

	// Iterate in a stable order so the returned include list, and therefore any
	// protoc invocation built from it, does not change between runs.
	repos := make([]string, 0, len(d.Items))
	for repo := range d.Items {
		repos = append(repos, repo)
	}
	sort.Strings(repos)

	var all []vendored
	for _, repo := range repos {
		repoVendored, err := d.downloadModule(repo, d.Items[repo])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", repo, err)
		}
		all = append(all, repoVendored...)
	}

	return all, nil
}

func (d *downloader) downloadModule(repo string, want []string) ([]vendored, error) {
	mod, err := resolve(d.Dir, repo)
	if err != nil {
		return nil, err
	}

	// A local replace is already a full checkout of the repository, so the
	// paths are usable in place. Vendoring one would only add a stale copy that
	// shadows the edits the replace exists to pick up.
	if mod.localReplace() {
		base := filepath.ToSlash(mod.Dir)
		found := make([]vendored, 0, len(want))
		for _, p := range want {
			found = append(found, vendored{
				Module:  repo,
				Path:    p,
				Dir:     filepath.Join(mod.Dir, filepath.FromSlash(p)),
				Include: anchor(base, p),
			})
		}
		return found, nil
	}

	// Key the vendor directory by version so bumping the dependency can never
	// be served out of a copy made for the previous one.
	vendorDir := filepath.Join(d.vendorDir(), escapePath(mod.Path)+"@"+mod.Version)

	// The module cache holds the module's subtree only, so anything reaching
	// above the module root has to come from a clone. So does anything the
	// cache is simply missing, which is the common case for .proto files that
	// are not part of the published Go package.
	subdir := ""
	needClone := escapesRoot(want) || !existsUnder(mod.Dir, want)
	if needClone {
		if _, subdir, err = splitRepo(mod.Path); err != nil {
			return nil, err
		}
	}

	// Resolve every requested path to a repository-relative path first: it is
	// both the layout under vendorDir and, once we have a source tree, the path
	// to copy from. Doing it up front means the vendor cache can be checked
	// without paying for a clone.
	rels := make([]string, 0, len(want))
	dirs := make([]string, 0, len(want))
	found := make([]vendored, 0, len(want))
	for _, p := range want {
		rel := path.Join(subdir, p)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, fmt.Errorf("path %q escapes the repository root", p)
		}
		dir := filepath.Join(vendorDir, filepath.FromSlash(rel))
		rels = append(rels, rel)
		dirs = append(dirs, dir)
		found = append(found, vendored{
			Module:  repo,
			Path:    p,
			Dir:     dir,
			Include: anchor(filepath.ToSlash(vendorDir)+"/"+subdir, p),
		})
	}

	if allExist(dirs) {
		return found, nil
	}

	srcRoot := mod.Dir
	if needClone {
		clone, err := cloneModule(mod)
		if err != nil {
			return nil, err
		}
		// The clone is only a staging area; the vendored copy is what survives.
		defer os.RemoveAll(clone)
		srcRoot = clone
	}

	for i, rel := range rels {
		src := filepath.Join(srcRoot, filepath.FromSlash(rel))
		if err := copyProtos(src, dirs[i]); err != nil {
			return nil, fmt.Errorf("vendoring %q: %w", want[i], err)
		}
	}

	return found, nil
}

// anchor returns the directory a path is resolved against once its leading
// ".." components have been applied, which is the directory protoc has to be
// given as -I for that path's imports to resolve.
//
// Import paths are written relative to the repository's proto root, not to the
// Go module: "../values" under module directory "cre/go" holds
// "values/v1/values.proto", so the include directory is "cre", not "cre/values".
func anchor(base, p string) string {
	dir := path.Clean(base)
	for part := range strings.SplitSeq(path.Clean(p), "/") {
		if part != ".." {
			break
		}
		dir = path.Dir(dir)
	}
	return filepath.FromSlash(dir)
}

// dedupe drops repeated entries, keeping the first of each. Sibling paths
// commonly share an anchor, and protoc has no use for the same -I twice.
func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := values[:0]
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// module is the subset of `go list -m -json` output we need.
type module struct {
	Path    string
	Version string
	Dir     string
	Replace *module
}

// localReplace reports whether the module is replaced by a directory on disk,
// which Go signals by a replacement with a path but no version.
func (m *module) localReplace() bool {
	return m.Replace != nil && m.Replace.Version == ""
}

// resolve asks the go tool where a module lives, following any replacement. An
// empty repo resolves the main module - the one being generated.
func resolve(dir, repo string) (*module, error) {
	args := []string{"list", "-m", "-json"}
	if repo != "" {
		args = append(args, "--", repo)
	}

	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%q is not in the build list, run `go mod tidy`: %w", repo, err)
	}

	mod := &module{}
	if err = json.Unmarshal(out, mod); err != nil {
		return nil, err
	}

	// Report the replacement's identity, but keep the replacement's own Dir:
	// that is where the source actually is.
	if mod.Replace != nil {
		replace := *mod.Replace
		if replace.Dir == "" {
			replace.Dir = mod.Dir
		}
		replace.Replace = mod.Replace
		mod = &replace
	}

	if mod.Dir == "" && !mod.localReplace() {
		return nil, fmt.Errorf("%q is not downloaded, run `go mod tidy`", repo)
	}

	return mod, nil
}

// escapesRoot reports whether any path points above the module root.
func escapesRoot(paths []string) bool {
	for _, p := range paths {
		if clean := path.Clean(p); clean == ".." || strings.HasPrefix(clean, "../") {
			return true
		}
	}
	return false
}

// existsUnder reports whether every path is present under root. Only meaningful
// once the paths are known not to escape root.
func existsUnder(root string, paths []string) bool {
	if root == "" {
		return false
	}

	full := make([]string, 0, len(paths))
	for _, p := range paths {
		full = append(full, filepath.Join(root, filepath.FromSlash(p)))
	}
	return allExist(full)
}

func allExist(paths []string) bool {
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// splitRepo splits a module path into the repository that hosts it and the
// directory the module occupies inside that repository.
//
// Only the well-known hosts are handled: full go-get discovery would need a
// network round trip to a page we cannot clone from anyway.
func splitRepo(modPath string) (root, subdir string, err error) {
	parts := strings.Split(modPath, "/")
	switch {
	case len(parts) < 3:
		return "", "", fmt.Errorf("cannot determine the repository for module %q", modPath)
	case parts[0] != "github.com" && parts[0] != "gitlab.com" && parts[0] != "bitbucket.org":
		return "", "", fmt.Errorf("cannot determine the repository for module %q, only github.com, gitlab.com and bitbucket.org are supported", modPath)
	}

	// A /vN major-version suffix is part of the module path, not the repository
	// layout, so it is never a real directory.
	if last := parts[len(parts)-1]; len(parts) > 3 && isMajorSuffix(last) {
		parts = parts[:len(parts)-1]
	}

	return strings.Join(parts[:3], "/"), strings.Join(parts[3:], "/"), nil
}

func isMajorSuffix(s string) bool {
	if !strings.HasPrefix(s, "v") || len(s) < 2 {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// cloneModule shallow-clones the repository the module lives in at the exact
// revision the build list pins, and returns the temporary checkout directory.
// The caller owns it.
func cloneModule(mod *module) (string, error) {
	root, subdir, err := splitRepo(mod.Path)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "proto-vendor-")
	if err != nil {
		return "", err
	}

	ref, named := gitRef(mod.Version, subdir)

	// A tag can be fetched on its own, so ask for nothing else.
	fetch := []string{"-C", dir, "fetch", "--quiet", "--no-tags", "--depth", "1", "origin", ref}
	checkout := "FETCH_HEAD"
	if !named {
		// A pseudo-version only carries an abbreviated commit, which a remote
		// refuses to resolve in a fetch request. Take every commit instead, but
		// no trees or blobs, and let the checkout fault in just the revision we
		// asked for.
		fetch = []string{"-C", dir, "fetch", "--quiet", "--filter=tree:0", "--no-tags", "origin"}
		checkout = ref
	}

	// git init plus a targeted fetch, rather than `git clone`, to avoid paying
	// for history and branches nothing here reads.
	steps := [][]string{
		{"init", "--quiet", dir},
		{"-C", dir, "remote", "add", "origin", "https://" + root},
		fetch,
		{"-C", dir, "checkout", "--quiet", checkout},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Stderr = os.Stderr
		if err = cmd.Run(); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}

	return dir, nil
}

// gitRef maps a module version to the revision that holds it, and reports
// whether that revision is a name the remote can look up. A pseudo-version
// yields its (abbreviated) commit; anything else is a tag, prefixed with the
// module's subdirectory when the module is not at the repository root.
func gitRef(version, subdir string) (ref string, named bool) {
	if m := pseudoVersion.FindStringSubmatch(version); m != nil {
		return m[1], false
	}

	version = strings.TrimSuffix(version, "+incompatible")
	if subdir == "" {
		return version, true
	}
	return subdir + "/" + version, true
}

// copyProtos copies the .proto files at src into dst, mirroring the directory
// structure. Everything else is left behind: only the schemas are needed, and
// the rest of a repository can be large.
func copyProtos(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return copyFile(src, dst)
	}

	return filepath.WalkDir(src, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}

		if entry.IsDir() {
			// .git in particular, but any dot directory is tooling, not schema.
			if rel != "." && strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}

		if !entry.Type().IsRegular() || filepath.Ext(p) != ".proto" {
			return nil
		}

		return copyFile(p, filepath.Join(dst, rel))
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}

// escapePath applies the module cache's case encoding, so that module paths
// differing only in case cannot collide on a case-insensitive file system.
func escapePath(modPath string) string {
	escaped := strings.Builder{}
	for _, r := range modPath {
		if r >= 'A' && r <= 'Z' {
			escaped.WriteByte('!')
			r += 'a' - 'A'
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
