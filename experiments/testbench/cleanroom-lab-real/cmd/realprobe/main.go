// realprobe 对真实媒体根做严格只读盘点，并输出不含路径与内容的脱敏结果。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

type rootFlags []string

func (r *rootFlags) String() string { return strings.Join(*r, ",") }
func (r *rootFlags) Set(v string) error {
	*r = append(*r, v)
	return nil
}

type rootSpec struct{ Alias, Path string }

type resultFile struct {
	SchemaVersion int          `json:"schema_version"`
	GeneratedAt   string       `json:"generated_at"`
	Roots         []rootResult `json:"roots"`
}

type rootResult struct {
	Alias                    string           `json:"alias"`
	GuardSHA256              string           `json:"guard_sha256"`
	Directories              int64            `json:"directories"`
	Files                    int64            `json:"files"`
	TotalBytes               int64            `json:"total_bytes"`
	MaxDepth                 int              `json:"max_depth"`
	MaxRelativePathChars     int              `json:"max_relative_path_chars"`
	EstimatedWorks           int64            `json:"estimated_works"`
	DirectoriesWithMetadata  int64            `json:"directories_with_metadata"`
	DirectoriesWithMedia     int64            `json:"directories_with_media"`
	MetadataFiles            int64            `json:"metadata_files"`
	MetadataValid            int64            `json:"metadata_valid"`
	MetadataInvalid          int64            `json:"metadata_invalid"`
	MetadataBytes            int64            `json:"metadata_bytes"`
	MetadataMaxBytes         int64            `json:"metadata_max_bytes"`
	MetadataTypes            map[string]int64 `json:"metadata_types"`
	TopMetadataPaths         map[string]int64 `json:"top_metadata_paths"`
	Extensions               map[string]int64 `json:"extensions"`
	Anomalies                map[string]int64 `json:"anomalies"`
	HashSamples              int              `json:"hash_samples"`
	FastFingerprintGroups    int              `json:"fast_fingerprint_groups"`
	ConfirmedDuplicateGroups int              `json:"confirmed_duplicate_groups"`
	FastCollisionGroups      int              `json:"fast_collision_groups"`
	ScanMilliseconds         int64            `json:"scan_milliseconds"`
}

type directoryFacts struct {
	metadata bool
	media    bool
}

type hashPair struct{ fast, full string }

var safeKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

var mediaExt = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "bmp": true, "tif": true, "tiff": true, "avif": true,
	"mp4": true, "webm": true, "mov": true, "mkv": true, "avi": true, "m4v": true,
}

var metadataExt = map[string]bool{"json": true, "yaml": true, "yml": true, "toml": true, "xml": true, "nfo": true}

func main() {
	var roots rootFlags
	flag.Var(&roots, "root", "repeatable alias=absolute-path")
	out := flag.String("out", "", "result JSON path outside every media root")
	hashSamples := flag.Int("hash-samples", 2000, "maximum media files to full-hash")
	metadataSamples := flag.Int("metadata-samples", 2000, "maximum JSON metadata files to parse")
	flag.Parse()
	if len(roots) < 1 || *out == "" {
		fatal(errors.New("at least one --root and --out are required"))
	}

	specs := make([]rootSpec, 0, len(roots))
	for _, raw := range roots {
		parts := strings.SplitN(raw, "=", 2)
		if len(parts) != 2 || parts[0] == "" || !filepath.IsAbs(parts[1]) {
			fatal(fmt.Errorf("invalid root specification: %q", raw))
		}
		clean := filepath.Clean(parts[1])
		if inside(clean, *out) {
			fatal(fmt.Errorf("output must not be inside media root alias %q", parts[0]))
		}
		specs = append(specs, rootSpec{parts[0], clean})
	}

	report := resultFile{SchemaVersion: 1, GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, spec := range specs {
		res, err := scan(spec, *hashSamples, *metadataSamples)
		if err != nil {
			fatal(fmt.Errorf("scan %s: %w", spec.Alias, err))
		}
		report.Roots = append(report.Roots, res)
		fmt.Printf("%s: dirs=%d files=%d metadata=%d works~%d guard=%s duration=%s\n",
			res.Alias, res.Directories, res.Files, res.MetadataFiles, res.EstimatedWorks, res.GuardSHA256[:12], time.Duration(res.ScanMilliseconds)*time.Millisecond)
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fatal(err)
	}
	tmp := *out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	err = enc.Encode(report)
	closeErr := f.Close()
	if err != nil {
		fatal(err)
	}
	if closeErr != nil {
		fatal(closeErr)
	}
	if err := os.Rename(tmp, *out); err != nil {
		fatal(err)
	}
}

func scan(spec rootSpec, hashLimit, metadataLimit int) (rootResult, error) {
	started := time.Now()
	res := rootResult{
		Alias: spec.Alias, MetadataTypes: map[string]int64{}, TopMetadataPaths: map[string]int64{},
		Extensions: map[string]int64{}, Anomalies: map[string]int64{},
	}
	guard := sha256.New()
	dirs := map[string]*directoryFacts{}
	hashes := make([]hashPair, 0, hashLimit)
	parsedMetadata := 0

	err := filepath.WalkDir(spec.Path, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			res.Anomalies["unreadable_entry"]++
			return nil
		}
		rel, err := filepath.Rel(spec.Path, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			res.Anomalies["unreadable_info"]++
			return nil
		}
		fmt.Fprintf(guard, "%s\x00%d\x00%d\x00%s\n", rel, info.Size(), info.ModTime().UnixNano(), info.Mode().String())
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, "/") + 1
		}
		if depth > res.MaxDepth {
			res.MaxDepth = depth
		}
		if len([]rune(rel)) > res.MaxRelativePathChars {
			res.MaxRelativePathChars = len([]rune(rel))
		}
		classifyName(rel, &res)
		if d.IsDir() {
			res.Directories++
			if _, ok := dirs[rel]; !ok {
				dirs[rel] = &directoryFacts{}
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			res.Anomalies["symlink"]++
		}
		res.Files++
		res.TotalBytes += info.Size()
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(d.Name()), "."))
		if ext == "" {
			ext = "<none>"
		}
		res.Extensions[ext]++
		parent := filepath.ToSlash(filepath.Dir(rel))
		fact := dirs[parent]
		if fact == nil {
			fact = &directoryFacts{}
			dirs[parent] = fact
		}
		if mediaExt[ext] {
			fact.media = true
			if len(hashes) < hashLimit {
				fast, full, err := fingerprints(path, info.Size())
				if err != nil {
					res.Anomalies["unreadable_hash_sample"]++
				} else {
					hashes = append(hashes, hashPair{fast, full})
				}
			}
		}
		if metadataExt[ext] {
			res.MetadataFiles++
			res.MetadataBytes += info.Size()
			if info.Size() > res.MetadataMaxBytes {
				res.MetadataMaxBytes = info.Size()
			}
			res.MetadataTypes[ext]++
			fact.metadata = true
			if ext == "json" && parsedMetadata < metadataLimit && info.Size() <= 4<<20 {
				parsedMetadata++
				data, err := os.ReadFile(path)
				if err != nil {
					res.MetadataInvalid++
				} else {
					var v any
					if json.Unmarshal(data, &v) != nil {
						res.MetadataInvalid++
					} else {
						res.MetadataValid++
						collectPaths(v, "", 0, res.TopMetadataPaths)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	for _, f := range dirs {
		if f.metadata {
			res.DirectoriesWithMetadata++
		}
		if f.media {
			res.DirectoriesWithMedia++
		}
	}
	if res.DirectoriesWithMetadata > 0 {
		res.EstimatedWorks = res.DirectoriesWithMetadata
	} else {
		res.EstimatedWorks = res.DirectoriesWithMedia
	}
	res.HashSamples = len(hashes)
	res.FastFingerprintGroups, res.ConfirmedDuplicateGroups, res.FastCollisionGroups = summarizeHashes(hashes)
	res.GuardSHA256 = hex.EncodeToString(guard.Sum(nil))
	res.TopMetadataPaths = topN(res.TopMetadataPaths, 150)
	res.Extensions = topN(res.Extensions, 100)
	res.ScanMilliseconds = time.Since(started).Milliseconds()
	return res, nil
}

func collectPaths(v any, prefix string, depth int, out map[string]int64) {
	if depth > 8 {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			safe := k
			if !safeKey.MatchString(k) {
				safe = "<dynamic>"
			}
			p := safe
			if prefix != "" {
				p = prefix + "." + safe
			}
			out[p]++
			collectPaths(x[k], p, depth+1, out)
		}
	case []any:
		p := prefix + "[]"
		out[p]++
		for i, item := range x {
			if i >= 3 {
				break
			}
			collectPaths(item, p, depth+1, out)
		}
	}
}

func fingerprints(path string, size int64) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	fullHash := sha256.New()
	if _, err := io.Copy(fullHash, f); err != nil {
		return "", "", err
	}
	first := make([]byte, min64(size, 64<<10))
	if _, err := f.ReadAt(first, 0); err != nil && err != io.EOF {
		return "", "", err
	}
	last := make([]byte, min64(size, 64<<10))
	if _, err := f.ReadAt(last, max64(0, size-int64(len(last)))); err != nil && err != io.EOF {
		return "", "", err
	}
	quick := sha256.New()
	fmt.Fprintf(quick, "%d\x00", size)
	quick.Write(first)
	quick.Write(last)
	return hex.EncodeToString(quick.Sum(nil)), hex.EncodeToString(fullHash.Sum(nil)), nil
}

func summarizeHashes(pairs []hashPair) (fastGroups, duplicateGroups, collisionGroups int) {
	byFast := map[string][]string{}
	for _, p := range pairs {
		byFast[p.fast] = append(byFast[p.fast], p.full)
	}
	for _, fulls := range byFast {
		if len(fulls) < 2 {
			continue
		}
		fastGroups++
		unique := map[string]int{}
		for _, full := range fulls {
			unique[full]++
		}
		if len(unique) > 1 {
			collisionGroups++
		}
		for _, n := range unique {
			if n > 1 {
				duplicateGroups++
				break
			}
		}
	}
	return
}

func classifyName(rel string, res *rootResult) {
	runes := []rune(rel)
	if len(runes) > 240 {
		res.Anomalies["path_over_240_chars"]++
	}
	if strings.ContainsAny(rel, "%[]{}#") {
		res.Anomalies["boundary_punctuation"]++
	}
	for _, r := range runes {
		if r > unicode.MaxASCII {
			res.Anomalies["non_ascii"]++
			if r > 0xFFFF {
				res.Anomalies["supplementary_unicode"]++
			}
			break
		}
		if unicode.IsControl(r) {
			res.Anomalies["control_character"]++
			break
		}
	}
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			res.Anomalies["trailing_dot_or_space"]++
		}
		upper := strings.ToUpper(strings.TrimSuffix(seg, filepath.Ext(seg)))
		if isReserved(upper) {
			res.Anomalies["windows_reserved_name"]++
		}
	}
}

func isReserved(s string) bool {
	if s == "CON" || s == "PRN" || s == "AUX" || s == "NUL" {
		return true
	}
	if len(s) == 4 && (strings.HasPrefix(s, "COM") || strings.HasPrefix(s, "LPT")) && s[3] >= '1' && s[3] <= '9' {
		return true
	}
	return false
}

func topN(in map[string]int64, n int) map[string]int64 {
	type kv struct {
		k string
		v int64
	}
	items := make([]kv, 0, len(in))
	for k, v := range in {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v == items[j].v {
			return items[i].k < items[j].k
		}
		return items[i].v > items[j].v
	})
	if len(items) > n {
		items = items[:n]
	}
	out := make(map[string]int64, len(items))
	for _, item := range items {
		out[item.k] = item.v
	}
	return out
}

func inside(root, candidate string) bool {
	rootAbs, _ := filepath.Abs(root)
	candidateAbs, _ := filepath.Abs(candidate)
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func min64(a int64, b int) int {
	if a < int64(b) {
		return int(a)
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
