// replace-needed changes the name of a shared library a dynamically linked ELF binary depends on (a DT_NEEDED entry),
// in place. Both names must have the same length, so the layout of the binary doesn't change: only the string in
// .dynstr is overwritten. Used by the .rpm build (see .goreleaser.yaml) to depend on libsensors.so.4 instead of
// libsensors.so.5; patchelf corrupts some of the binaries (e.g. s390x).
//
// Usage: go run .github/scripts/replace-needed.go <binary> <old library> <new library>
package main

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"slices"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: replace-needed <binary> <old library> <new library>")
		os.Exit(2)
	}
	if err := replaceNeeded(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintf(os.Stderr, "replace-needed: %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func replaceNeeded(path, oldLib, newLib string) error {
	if len(oldLib) != len(newLib) {
		return fmt.Errorf("%q and %q differ in length", oldLib, newLib)
	}

	f, err := elf.Open(path)
	if err != nil {
		return err
	}
	needed, err := f.ImportedLibraries()
	if err != nil {
		f.Close()
		return err
	}
	if !slices.Contains(needed, oldLib) {
		f.Close()
		return fmt.Errorf("doesn't depend on %s (depends on %v)", oldLib, needed)
	}
	// versioned symbols name their library in .gnu.version_r, which would still point at the old name
	symbols, err := f.ImportedSymbols()
	if err != nil {
		f.Close()
		return err
	}
	for _, s := range symbols {
		if s.Library == oldLib {
			f.Close()
			return fmt.Errorf("symbol %s@%s is versioned, renaming the library is not enough", s.Name, s.Version)
		}
	}
	dynstr := f.Section(".dynstr")
	if dynstr == nil {
		f.Close()
		return fmt.Errorf("no .dynstr section")
	}
	data, err := dynstr.Data()
	offset := int64(dynstr.Offset)
	f.Close()
	if err != nil {
		return err
	}

	// the name as a complete string: preceded and followed by NUL
	needle := []byte("\x00" + oldLib + "\x00")
	if n := bytes.Count(data, needle); n != 1 {
		return fmt.Errorf("found %s %d times in .dynstr, expected once", oldLib, n)
	}
	pos := int64(bytes.Index(data, needle)) + 1

	out, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := out.WriteAt([]byte(newLib), offset+pos); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	f, err = elf.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	needed, err = f.ImportedLibraries()
	if err != nil {
		return err
	}
	if slices.Contains(needed, oldLib) || !slices.Contains(needed, newLib) {
		return fmt.Errorf("replacing failed, depends on %v", needed)
	}
	return nil
}
