package packagerepository

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"io"
	"io/fs"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
)

type ResourceReference struct {
	Field       string `json:"field"`
	Language    string `json:"language"`
	PackagePath string `json:"packagePath"`
	Size        int64  `json:"size"`
	Sha512      string `json:"sha512"`
}
type Projection struct {
	Metadata  PackageMetadata     `json:"metadata"`
	Resources []ResourceReference `json:"resources"`
}
type ProjectionLimits struct{ MaxAssetBytes int64 }
type ProjectionOptions struct{ Limits ProjectionLimits }
type Publication struct {
	DownloadURL    string
	DownloadSha512 string
}
type AssetLayout map[string]string
type AssetMapping struct {
	ResourceReference
	RepositoryPath string `json:"repositoryPath"`
}
type Difference struct {
	Pointer  string `json:"pointer"`
	Kind     string `json:"kind"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
}

func normalize(m PackageMetadata) PackageMetadata {
	m.Dependencies = append([]packageinfo.Dependency{}, m.Dependencies...)
	m.Singers = append([]SingerSummary{}, m.Singers...)
	sort.Slice(m.Dependencies, func(i, j int) bool { return m.Dependencies[i].ID < m.Dependencies[j].ID })
	sort.Slice(m.Singers, func(i, j int) bool { return m.Singers[i].ID < m.Singers[j].ID })
	return m
}
func project(ctx context.Context, files fs.FS, root string, limit int64) (Projection, error) {
	var out Projection
	p, err := packageinfo.Load(files, root)
	if err != nil {
		return out, err
	}
	m := PackageMetadata{Identity: p.Identity, CompatVersion: p.CompatVersion, Name: p.Name, Description: p.Description, Vendor: p.Vendor, URL: p.URL, Dependencies: p.Dependencies, Singers: []SingerSummary{}}
	for _, field := range []string{"name", "description", "vendor", "url", "license", "readme"} {
		if v, ok := p.Manifest[field]; ok {
			if field == "url" {
				if _, ok := v.(string); !ok {
					return out, fmt.Errorf("The publication URL is invalid.")
				}
			} else {
				if _, ok := packageinfo.TextValue(v); !ok {
					return out, fmt.Errorf("The publication field %s is invalid.", field)
				}
			}
		}
	}
	for _, c := range p.Contributions {
		if c.Category != "singer" {
			continue
		}
		if c.SingerDeclaration == nil || !c.Expanded || c.MetadataError != "" || c.ModuleMetadata == nil || !validText(c.Name) {
			return out, fmt.Errorf("The singer publication metadata for %s is invalid.", c.ID)
		}
		m.Singers = append(m.Singers, SingerSummary{c.ID, c.Name, c.Interface, c.Variant, c.Level})
	}
	out.Resources = []ResourceReference{}
	var total int64
	for _, f := range []struct {
		name  string
		paths packageinfo.Text
		dest  *packageinfo.Text
	}{{"license", p.License, &m.License}, {"readme", p.Readme, &m.Readme}} {
		if f.paths == nil {
			continue
		}
		*f.dest = packageinfo.Text{}
		for language, p := range f.paths {
			if err = ctx.Err(); err != nil {
				return out, err
			}
			rel, e := filepath.Rel(root, p)
			if e != nil {
				return out, e
			}
			rel = filepath.ToSlash(rel)
			if err = PortablePath(rel); err != nil {
				return out, err
			}
			file, e := files.Open(rel)
			if e != nil {
				return out, e
			}
			st, e := file.Stat()
			if e != nil || !st.Mode().IsRegular() {
				file.Close()
				return out, fmt.Errorf("The resource %s must be a regular file.", rel)
			}
			if st.Size() > limit-total {
				file.Close()
				return out, fmt.Errorf("The asset bytes exceed their budget.")
			}
			h := sha512.New()
			n, e := io.Copy(h, io.LimitReader(file, limit-total+1))
			file.Close()
			if e != nil {
				return out, e
			}
			total += n
			if total > limit {
				return out, fmt.Errorf("The asset bytes exceed their budget.")
			}
			(*f.dest)[language] = rel
			out.Resources = append(out.Resources, ResourceReference{f.name, language, rel, n, hex.EncodeToString(h.Sum(nil))})
		}
	}
	out.Metadata = normalize(m)
	sort.Slice(out.Resources, func(i, j int) bool {
		a, b := out.Resources[i], out.Resources[j]
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		return a.Language < b.Language
	})
	return out, nil
}
func ProjectFS(ctx context.Context, files fs.FS, o ProjectionOptions) (Projection, error) {
	roots := [2]string{"/__dspm_projection_a__", "/__dspm_projection_b__"}
	if runtime.GOOS == "windows" {
		roots = [2]string{`C:\__dspm_projection_a__`, `C:\__dspm_projection_b__`}
	}
	if o.Limits.MaxAssetBytes == 0 {
		o.Limits.MaxAssetBytes = 4 << 20
	}
	a, err := project(ctx, files, roots[0], o.Limits.MaxAssetBytes)
	if err != nil {
		return a, err
	}
	b, err := project(ctx, files, roots[1], o.Limits.MaxAssetBytes)
	if err != nil {
		return a, err
	}
	if !reflect.DeepEqual(a, b) {
		return a, fmt.Errorf("NONPORTABLE_METADATA: Publication metadata depends on the virtual root.")
	}
	return a, nil
}
func BuildManifest(p Projection, pub Publication, layout AssetLayout) (RemoteManifest, []AssetMapping, error) {
	m := RemoteManifest{RepositoryFormatVersion: 1, PackageMetadata: normalize(p.Metadata), DownloadURL: pub.DownloadURL, DownloadSha512: pub.DownloadSha512}
	m.License = nil
	m.Readme = nil
	assets := []AssetMapping{}
	for _, r := range p.Resources {
		dst := p.Metadata.Version.String() + "/" + r.PackagePath
		if v, ok := layout[r.PackagePath]; ok {
			dst = v
		}
		if err := PortablePath(dst); err != nil {
			return m, nil, err
		}
		if r.Field == "license" {
			if m.License == nil {
				m.License = packageinfo.Text{}
			}
			m.License[r.Language] = dst
		} else {
			if m.Readme == nil {
				m.Readme = packageinfo.Text{}
			}
			m.Readme[r.Language] = dst
		}
		assets = append(assets, AssetMapping{r, dst})
	}
	if _, err := MarshalManifest(m); err != nil {
		return m, nil, err
	}
	return m, assets, nil
}
func ResourceMappings(m RemoteManifest, p Projection) ([]AssetMapping, error) {
	out := []AssetMapping{}
	for _, r := range p.Resources {
		paths := m.License
		if r.Field == "readme" {
			paths = m.Readme
		}
		dst, ok := paths[r.Language]
		if !ok {
			return nil, fmt.Errorf("A required resource language is missing.")
		}
		if err := PortablePath(dst); err != nil {
			return nil, err
		}
		out = append(out, AssetMapping{r, dst})
	}
	return out, nil
}
func assetKeys(t packageinfo.Text) packageinfo.Text {
	if t == nil {
		return nil
	}
	out := packageinfo.Text{}
	for k := range t {
		out[k] = ""
	}
	return out
}
func CompareMetadata(m RemoteManifest, p Projection) ([]Difference, error) {
	a, b := normalize(p.Metadata), normalize(m.PackageMetadata)
	a.License = assetKeys(a.License)
	a.Readme = assetKeys(a.Readme)
	b.License = assetKeys(b.License)
	b.Readme = assetKeys(b.Readme)
	for _, t := range []packageinfo.Text{m.License, m.Readme} {
		for _, s := range t {
			if err := PortablePath(s); err != nil {
				return nil, err
			}
		}
	}
	return Differences(a, b), nil
}

// Returns whether immutable package semantics match.
// Replica URLs and asset paths may differ. Identities, archive digests, and language sets must match.
func SameContent(a, b RemoteManifest) bool {
	d, err := CompareMetadata(a, Projection{Metadata: b.PackageMetadata})
	return err == nil && len(d) == 0 && a.DownloadSha512 == b.DownloadSha512
}
func Differences(expected, actual any) []Difference {
	toValue := func(v any) any {
		b, _ := json.Marshal(v)
		var out any
		d := json.NewDecoder(strings.NewReader(string(b)))
		d.UseNumber()
		_ = d.Decode(&out)
		return out
	}
	out := []Difference{}
	var walk func(string, any, any)
	walk = func(pointer string, a, b any) {
		if reflect.DeepEqual(a, b) {
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			sorted := []string{}
			for k := range keys {
				sorted = append(sorted, k)
			}
			sort.Strings(sorted)
			for _, k := range sorted {
				av, ae := am[k]
				bv, be := bm[k]
				p := pointer + "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
				if !ae || !be {
					out = append(out, Difference{p, "presence", map[string]any{"present": ae, "value": av}, map[string]any{"present": be, "value": bv}})
				} else {
					walk(p, av, bv)
				}
			}
			return
		}
		aa, aok := a.([]any)
		ba, bok := b.([]any)
		if aok && bok && len(aa) == len(ba) {
			for i := range aa {
				walk(fmt.Sprintf("%s/%d", pointer, i), aa[i], ba[i])
			}
			return
		}
		out = append(out, Difference{pointer, "value", a, b})
	}
	walk("", toValue(expected), toValue(actual))
	return out
}
func CompareAssetBytes(ctx context.Context, mappings []AssetMapping, repositoryAssets fs.FS) ([]Difference, error) {
	out := []Difference{}
	for _, m := range mappings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := repositoryAssets.Open(m.RepositoryPath)
		if err != nil {
			return nil, err
		}
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			f.Close()
			return nil, fmt.Errorf("The asset is invalid.")
		}
		h := sha512.New()
		n, err := io.Copy(h, io.LimitReader(f, m.Size+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		actual := hex.EncodeToString(h.Sum(nil))
		if n != m.Size || actual != m.Sha512 {
			out = append(out, Difference{"/" + m.Field + "/" + m.Language, "asset-bytes", m.Sha512, actual})
		}
	}
	return out, nil
}
