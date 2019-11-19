package main // import "lukechampine.com/user"

import (
	"log"
	"os"
	"runtime"
	"syscall"

	"github.com/pkg/errors"
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"lukechampine.com/flagg"
	"lukechampine.com/muse"
	"lukechampine.com/us/hostdb"
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

func getCurrentHeight() (types.BlockHeight, error) {
	if config.MuseAddr == "" {
		log.Fatal("Could not get contracts: no muse server specified")
	}
	return renterutil.NewSHARDClient(config.MuseAddr + "/shard").ChainHeight()
}

type mapHKR map[hostdb.HostPublicKey]modules.NetAddress

func (m mapHKR) ResolveHostKey(hpk hostdb.HostPublicKey) (modules.NetAddress, error) {
	addr, ok := m[hpk]
	if !ok {
		return "", errors.New("no record of that host")
	}
	return addr, nil
}

func getContracts() (renter.ContractSet, renter.HostKeyResolver) {
	if config.MuseAddr == "" {
		log.Fatal("Could not get contracts: no muse server specified")
	}
	c := muse.NewClient(config.MuseAddr)
	contracts, err := c.Contracts()
	check("Could not get contracts:", err)
	set := make(renter.ContractSet, len(contracts))
	hkr := make(mapHKR, len(contracts))
	for _, c := range contracts {
		set[c.HostKey] = c.Contract
		hkr[c.HostKey] = c.HostAddress
	}
	return set, hkr
}

func makeHostSet() *renterutil.HostSet {
	contracts, hkr := getContracts()
	currentHeight, err := getCurrentHeight()
	check("Could not get current height:", err)
	hs := renterutil.NewHostSet(hkr, currentHeight)
	for _, c := range contracts {
		hs.AddHost(c)
	}
	return hs
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
			err = uploadmetadir(f.Name(), meta, makeHostSet(), config.MinShards)
		} else if _, statErr := os.Stat(meta); !os.IsNotExist(statErr) {
			err = resumeuploadmetafile(f, makeHostSet(), meta)
		} else {
			err = uploadmetafile(f, config.MinShards, makeHostSet(), meta)
		}
		f.Close()
		check("Upload failed:", err)

	case downloadCmd:
		f, meta := parseDownload(args, downloadCmd)
		var err error
		if stat, statErr := f.Stat(); statErr == nil && stat.IsDir() {
			err = downloadmetadir(f.Name(), makeHostSet(), meta)
		} else if f == os.Stdout {
			err = downloadmetastream(f, makeHostSet(), meta)
			// if the pipe we're writing to breaks, it was probably
			// intentional (e.g. 'head' exiting after reading 10 lines), so
			// suppress the error.
			if pe, ok := errors.Cause(err).(*os.PathError); ok {
				if errno, ok := pe.Err.(syscall.Errno); ok && errno == syscall.EPIPE {
					err = nil
				}
			}
		} else {
			err = downloadmetafile(f, makeHostSet(), meta)
			f.Close()
		}
		check("Download failed:", err)

	case checkupCmd:
		path := parseCheckup(args, checkupCmd)
		_, err := renter.ReadMetaIndex(path)
		check("Could not load metafile:", err)
		contracts, hkr := getContracts()
		err = checkupMeta(contracts, hkr, path)
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
			err = migrateLocal(f, makeHostSet(), meta)
			f.Close()
		case *mLocal != "" && isDir:
			err = migrateDirLocal(*mLocal, makeHostSet(), meta)
		case *mRemote && !isDir:
			err = migrateRemote(makeHostSet(), meta)
		case *mRemote && isDir:
			err = migrateDirRemote(makeHostSet(), meta)
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
		err := serve(makeHostSet(), args[0], *sAddr)
		if err != nil {
			log.Fatal(err)
		}

	case mountCmd:
		if len(args) != 2 {
			mountCmd.Usage()
			return
		}
		if config.MinShards == 0 {
			log.Fatalln(`Upload failed: minimum number of shards not specified.
Define min_shards in your config file or supply the -m flag.`)
		}
		err := mount(makeHostSet(), args[0], args[1], config.MinShards)
		if err != nil {
			log.Fatal(err)
		}

	case gcCmd:
		if len(args) != 1 {
			gcCmd.Usage()
			return
		}
		contracts, hkr := getContracts()
		err := deleteUnreferencedSectors(contracts, hkr, args[0])
		check("Garbage collection failed:", err)
	}
}
