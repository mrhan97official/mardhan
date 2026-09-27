// Package history summarises what an update changed by comparing the new
// ZIP with the last successful one, file by file (path + CRC32 + size from
// the ZIP directory, so nothing is decompressed).
package history

import (
	"archive/zip"
	"bytes"
	"path"
	"sort"
	"strings"
)

const listLimit = 100

type Changes struct {
	First         bool     `json:"first"`          // no earlier successful ZIP to compare with
	Files         int      `json:"files"`          // files in the new ZIP
	AddedCount    int      `json:"added_count"`
	ModifiedCount int      `json:"modified_count"`
	RemovedCount  int      `json:"removed_count"`
	Added         []string `json:"added"`
	Modified      []string `json:"modified"`
	Removed       []string `json:"removed"`
	Unreadable    bool     `json:"unreadable,omitempty"`
}

type fingerprint struct {
	crc  uint32
	size uint64
}

func ignored(name string) bool {
	base := path.Base(name)
	return strings.HasPrefix(name, "__MACOSX/") || base == ".DS_Store" || base == "Thumbs.db" ||
		strings.Contains("/"+name, "/node_modules/") || strings.Contains("/"+name, "/.next/") || strings.Contains("/"+name, "/.git/")
}

// index maps project-relative paths to fingerprints. A single wrapping
// folder (project-v2/...) is stripped so renamed ZIP roots still compare.
func index(data []byte) (map[string]fingerprint, bool) {
	if len(data) == 0 { return nil, false }
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil { return nil, false }
	raw := map[string]fingerprint{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() { continue }
		name := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(file.Name, "\\", "/")), "/")
		if name == "" || ignored(name) { continue }
		raw[name] = fingerprint{crc: file.CRC32, size: file.UncompressedSize64}
	}
	root := ""
	for name := range raw {
		first := strings.SplitN(name, "/", 2)
		if len(first) < 2 { root = ""; break }
		if root == "" { root = first[0] } else if root != first[0] { root = ""; break }
	}
	if root == "" { return raw, true }
	out := make(map[string]fingerprint, len(raw))
	for name, value := range raw { out[strings.TrimPrefix(name, root+"/")] = value }
	return out, true
}

func capped(list []string) []string {
	sort.Strings(list)
	if len(list) > listLimit { return list[:listLimit] }
	return list
}

// Diff compares the new ZIP with the previous successful ZIP (may be nil).
func Diff(newZip, oldZip []byte) Changes {
	current, ok := index(newZip)
	result := Changes{Added: []string{}, Modified: []string{}, Removed: []string{}}
	if !ok { result.Unreadable = true; return result }
	result.Files = len(current)
	previous, hasPrevious := index(oldZip)
	if !hasPrevious {
		result.First = true
		return result
	}
	for name, value := range current {
		old, exists := previous[name]
		switch {
		case !exists: result.Added = append(result.Added, name)
		case old != value: result.Modified = append(result.Modified, name)
		}
	}
	for name := range previous {
		if _, exists := current[name]; !exists { result.Removed = append(result.Removed, name) }
	}
	result.AddedCount, result.ModifiedCount, result.RemovedCount = len(result.Added), len(result.Modified), len(result.Removed)
	result.Added, result.Modified, result.Removed = capped(result.Added), capped(result.Modified), capped(result.Removed)
	return result
}
