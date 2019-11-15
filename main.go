package main // import "lukechampine.com/user"

import (
	"log"
	"os"
	"runtime"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"gitlab.com/NebulousLabs/Sia/build"
	"lukechampine.com/flagg"
	"lukechampine.com/muse"
	"lukechampine.com/us/renter"
	"lukechampine.com/us/renter/renterutil"
)

var (
	// to be supplied at build time
	githash   = "?"
	builddate = "?"
)

var (
	rootUsage = `Usage:
    user [flags] [action]

Actions:
    upload          upload a file
    download        download a file
    checkup         check the health of a file
    migrate         migrate a file to different hosts
    info            display info about a file
`
	versionUsage = rootUsage

	uploadUsage = `Usage:
    user upload file
    user upload file metafile
    user upload file folder
    user upload folder metafolder

Uploads the specified file or folder, storing its metadata in the specified
metafile or as multiple metafiles within the metafolder. The structure of the
metafolder will mirror that of the folder.

If the first argument is a single file and the second is a folder, the
metafile will be stored within folder, using the filename file.usa. For
example, 'user upload foo.txt bar/' will create the metafile 'bar/foo.txt.usa'.

If the destination is unspecified, it is assumed to be the current directory.
For example, 'user upload foo.txt' will create the metafile 'foo.txt.usa'.
`
	downloadUsage = `Usage:
    user download metafile
    user download metafile file
    user download metafile folder
    user download metafolder folder

Downloads the specified metafile or metafolder, storing file data in the
specified file or as multiple files within the folder. The structure of the
folder will mirror that of the metafolder.

If the first argument is a single metafile and the second is a folder, the
file data will be stored within the folder. This form requires that the
metafile have a .usa extension. The destination filename will be the metafile
without the .usa extension. For example, 'user download foo.txt.usa bar/' will
download to 'bar/foo.txt'.

If the destination is unspecified, it is assumed to be the current directory.
For example, 'user download foo.txt.usa' will download to 'foo.txt'.

However, if the destination file is unspecified and stdout is redirected (e.g.
via a pipe), the downloaded file will be written to stdout. For example,
'user download foo.txt.usa | cat' will display the file in the terminal.
`
	checkupUsage = `Usage:
    user checkup metafile
    user checkup contract

Verifies that a randomly-selected sector of the specified metafile or contract
is retrievable, and reports the resulting metrics for each host. Note that
this operation is not free.
`

	migrateUsage = `Usage:
    user migrate metafile
    user migrate metafolder

Migrates sector data from the metafile's current set of hosts to a new set.
There are three migration strategies, specified by mutually-exclusive flags.
`
	mLocalUsage = `Erasure-encode the original file on disk.`

	mRemoteUsage = `Download the file from existing hosts and erasure-encode it.
(The file will not be stored on disk at any point.)`

	infoUsage = `Usage:
    user info contract
    user info metafile

Displays information about the specified contract or metafile.
`
	serveUsage = `Usage:
    user serve metafolder

Serve the files in metafolder over HTTP.
`
	mountUsage = `Usage:
    user mount metafolder folder

Mount metafolder as a read-only FUSE filesystem, rooted at folder.
`
	convertUsage = `Usage:
    user convert contract

Converts a v1 contract to v2. If conversion fails, the v1 contract is not
affected.
`
	gcUsage = `Usage:
    user gc metafolder

Runs a "garbage collection cycle," which deletes any sectors not referenced
by the metafiles in the specified folder. Metafiles outside this folder may
become unavailable, so exercise caution when running this command!
`
)

var usage = flagg.SimpleUsage(flagg.Root, rootUsage) // point-free style!

func check(ctx string, err error) {
	if err != nil {
		log.Fatalln(ctx, err)
	}
}

func makeSHARDClient() *renterutil.SHARDClient {
	if config.SHARDAddr == "" {
		log.Fatal("Could not connect to SHARD server: no SHARD server specified")
	}
	return renterutil.NewSHARDClient(config.SHARDAddr)
}

func getContracts() renter.ContractSet {
	if config.MuseAddr == "" {
		log.Fatal("Could not get contracts: no muse server specified")
	}
	c := muse.NewClient(config.MuseAddr)
	contracts, err := c.Contracts()
	check("Could not get contracts:", err)
	set := make(renter.ContractSet, len(contracts))
	for _, c := range contracts {
		set[c.HostKey] = renter.Contract{
			HostKey: c.HostKey,
			ID:      c.ID,
			Key:     c.RenterKey,
		}
	}
	return set
}

func main() {
	log.SetFlags(0)

	err := loadConfig()
	if err != nil {
		check("Could not load config file:", err)
	}

	rootCmd := flagg.Root
	rootCmd.Usage = flagg.SimpleUsage(rootCmd, rootUsage)

	versionCmd := flagg.New("version", versionUsage)
	uploadCmd := flagg.New("upload", uploadUsage)
	uploadCmd.IntVar(&config.MinShards, "m", config.MinShards, "minimum number of shards required to download file")
	downloadCmd := flagg.New("download", downloadUsage)
	checkupCmd := flagg.New("checkup", checkupUsage)
	migrateCmd := flagg.New("migrate", migrateUsage)
	mLocal := migrateCmd.String("local", "", mLocalUsage)
	mRemote := migrateCmd.Bool("remote", false, mRemoteUsage)
	infoCmd := flagg.New("info", infoUsage)
	serveCmd := flagg.New("serve", serveUsage)
	sAddr := serveCmd.String("addr", ":8080", "HTTP service address")
	mountCmd := flagg.New("mount", mountUsage)
	mountCmd.IntVar(&config.MinShards, "m", config.MinShards, "minimum number of shards required to download files")
	convertCmd := flagg.New("convert", convertUsage)
	gcCmd := flagg.New("gc", gcUsage)

	cmd := flagg.Parse(flagg.Tree{
		Cmd: rootCmd,
		Sub: []flagg.Tree{
			{Cmd: versionCmd},
			{Cmd: uploadCmd},
			{Cmd: downloadCmd},
			{Cmd: checkupCmd},
			{Cmd: migrateCmd},
			{Cmd: infoCmd},
			{Cmd: serveCmd},
			{Cmd: mountCmd},
			{Cmd: convertCmd},
			{Cmd: gcCmd},
		},
	})
	args := cmd.Args()

	switch cmd {
	case rootCmd:
		if len(args) > 0 {
			usage()
			return
		}
		fallthrough
	case versionCmd:
		log.Printf("user v0.7.0\nCommit:     %s\nRelease:    %s\nGo version: %s %s/%s\nBuild Date: %s\n",
			githash, build.Release, runtime.Version(), runtime.GOOS, runtime.GOARCH, builddate)

	case uploadCmd:
		if config.MinShards == 0 {
			log.Fatalln(`Upload failed: minimum number of shards not specified.
Define min_shards in your config file or supply the -m flag.`)
		}
		f, meta := parseUpload(args, uploadCmd)
		var err error
		if stat, statErr := f.Stat(); statErr == nil && stat.IsDir() {
			err = uploadmetadir(f.Name(), meta, getContracts(), config.MinShards)
		} else if _, statErr := os.Stat(meta); !os.IsNotExist(statErr) {
			err = resumeuploadmetafile(f, getContracts(), meta)
		} else {
			err = uploadmetafile(f, config.MinShards, getContracts(), meta)
		}
		f.Close()
		check("Upload failed:", err)

	case downloadCmd:
		f, meta := parseDownload(args, downloadCmd)
		var err error
		if stat, statErr := f.Stat(); statErr == nil && stat.IsDir() {
			err = downloadmetadir(f.Name(), getContracts(), meta)
		} else if f == os.Stdout {
			err = downloadmetastream(f, getContracts(), meta)
			// if the pipe we're writing to breaks, it was probably
			// intentional (e.g. 'head' exiting after reading 10 lines), so
			// suppress the error.
			if pe, ok := errors.Cause(err).(*os.PathError); ok {
				if errno, ok := pe.Err.(syscall.Errno); ok && errno == syscall.EPIPE {
					err = nil
				}
			}
		} else {
			err = downloadmetafile(f, getContracts(), meta)
			f.Close()
		}
		check("Download failed:", err)

	case checkupCmd:
		path := parseCheckup(args, checkupCmd)
		_, err := renter.ReadMetaIndex(path)
		check("Could not load metafile:", err)
		err = checkupMeta(getContracts(), path)
		check("Checkup failed:", err)

	case migrateCmd:
		if len(args) == 0 {
			migrateCmd.Usage()
			return
		}
		meta := args[0]
		stat, statErr := os.Stat(meta)
		isDir := statErr == nil && stat.IsDir()
		var err error
		switch {
		case *mLocal == "" && !*mRemote:
			log.Fatalln("No migration strategy specified (see user migrate --help).")
		case *mLocal != "" && !isDir:
			f, ferr := os.Open(*mLocal)
			check("Could not open file:", ferr)
			err = migrateLocal(f, getContracts(), meta)
			f.Close()
		case *mLocal != "" && isDir:
			err = migrateDirLocal(*mLocal, getContracts(), meta)
		case *mRemote && !isDir:
			err = migrateRemote(getContracts(), meta)
		case *mRemote && isDir:
			err = migrateDirRemote(getContracts(), meta)
		default:
			log.Fatalln("Multiple migration strategies specified (see user migrate --help).")
		}
		check("Migration failed:", err)

	case infoCmd:
		if len(args) != 1 {
			infoCmd.Usage()
			return
		}
		m, err := renter.ReadMetaFile(args[0])
		check("Could not read metafile:", err)
		metainfo(m)

	case serveCmd:
		if len(args) != 1 {
			serveCmd.Usage()
			return
		}
		err := serve(getContracts(), args[0], *sAddr)
		if err != nil {
			log.Fatal(err)
		}

	case mountCmd:
		if len(args) != 2 {
			mountCmd.Usage()
			return
		}
		err := mount(getContracts(), args[0], args[1], config.MinShards)
		if err != nil {
			log.Fatal(err)
		}

	case gcCmd:
		if len(args) != 1 {
			gcCmd.Usage()
			return
		}
		err := deleteUnreferencedSectors(getContracts(), args[0])
		check("Garbage collection failed:", err)
	}
}
