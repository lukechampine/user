package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// assume metafiles have this extension
const metafileExt = ".usa"

// detect whether the user is redirecting stdin or stdout
// (not perfect; doesn't work with e.g. /dev/zero)
func isCharDevice(f *os.File) bool {
	stat, _ := f.Stat()
	return (stat.Mode() & os.ModeCharDevice) != 0
}

var redirStdout = !isCharDevice(os.Stdout)

// upload [file]
// upload [file] [metafile]
func parseUpload(args []string, cmd *flag.FlagSet) (file *os.File, metaPath string) {
	if !(len(args) == 1 || len(args) == 2) {
		cmd.Usage()
		os.Exit(2)
	}
	if len(args) == 1 {
		args = append(args, ".")
	}
	f, err := os.Open(args[0])
	check("Could not open file:", err)
	stat0, err := f.Stat()
	check("Could not stat file:", err)
	stat1, err := os.Stat(args[1])
	if err == nil && stat1.IsDir() && !stat0.IsDir() {
		// if [metafile] is a folder, and [file] is not a folder, assume that
		// the user wants to create a metafile named [metafile]/[file].usa
		args[1] = filepath.Join(args[1], filepath.Base(args[0])+metafileExt)
	}
	return f, args[1]
}

// download [metafile]
// download [metafile] [file]
// download [metafolder] [folder]
func parseDownload(args []string, cmd *flag.FlagSet) (file *os.File, metaPath string) {
	if !(len(args) == 1 || len(args) == 2) {
		cmd.Usage()
		os.Exit(2)
	}
	metaPath = args[0]
	if len(args) == 1 {
		if redirStdout {
			return os.Stdout, args[0]
		}
		args = append(args, ".")
	}
	isDir := func(path string) bool {
		stat, err := os.Stat(path)
		return err == nil && stat.IsDir()
	}
	var err error
	srcIsDir, dstIsDir := isDir(metaPath), isDir(args[1])
	switch {
	case srcIsDir && dstIsDir:
		file, err = os.Open(args[1])
		check("Could not open destination folder:", err)
	case srcIsDir && !dstIsDir:
		cmd.Usage()
		os.Exit(2)
	case !srcIsDir && dstIsDir:
		metabase := filepath.Base(args[0])
		if !strings.HasSuffix(metabase, metafileExt) {
			log.Fatalf("Could not infer download destination: metafile path does not end in %v", metafileExt)
		}
		args[1] = filepath.Join(args[1], strings.TrimSuffix(metabase, metafileExt))
		fallthrough
	case !srcIsDir && !dstIsDir:
		file, err = os.OpenFile(args[1], os.O_CREATE|os.O_RDWR, 0666)
		check("Could not create file:", err)
	}
	return file, metaPath
}

// checkup [metafile]
func parseCheckup(args []string, cmd *flag.FlagSet) (metaPath string) {
	if len(args) != 1 {
		cmd.Usage()
		os.Exit(2)
	}
	return args[0]
}
