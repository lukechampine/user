package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"gitlab.com/NebulousLabs/Sia/types"
	"lukechampine.com/us/merkle"
	"lukechampine.com/us/renter"
	"lukechampine.com/us/renter/renterutil"
	"lukechampine.com/us/renterhost"
)

func metainfo(m *renter.MetaFile) {
	var uploaded int64
	for _, shard := range m.Shards {
		for _, s := range shard {
			uploaded += int64(s.NumSegments * merkle.SegmentSize)
		}
	}
	redundancy := float64(len(m.Hosts)) / float64(m.MinShards)
	pctFullRedundancy := 100 * float64(uploaded) / (float64(m.Filesize) * redundancy)
	if m.Filesize == 0 || pctFullRedundancy > 100 {
		pctFullRedundancy = 100
	}

	fmt.Printf(`Filesize:   %v
Redundancy: %v-of-%v (%0.2gx replication)
Uploaded:   %v (%0.2f%% of full redundancy)
`, filesizeUnits(m.Filesize), m.MinShards, len(m.Hosts), redundancy,
		filesizeUnits(uploaded), pctFullRedundancy)
	fmt.Println("Hosts:")
	for _, hostKey := range m.Hosts {
		fmt.Printf("    %v\n", hostKey)
	}
}

// filesize returns a string that displays a filesize in human-readable units.
func filesizeUnits(size int64) string {
	if size == 0 {
		return "0 B"
	}
	sizes := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
	i := int(math.Log10(float64(size)) / 3)
	// printf trick: * means "print to 'i' digits"
	// so we get 1 decimal place for KB, 2 for MB, 3 for GB, etc.
	return fmt.Sprintf("%.*f %s", i, float64(size)/math.Pow10(3*i), sizes[i])
}

func makeHostSet(contracts renter.ContractSet, hkr renter.HostKeyResolver, currentHeight types.BlockHeight) *renterutil.HostSet {
	hs := renterutil.NewHostSet(hkr, currentHeight)
	for _, c := range contracts {
		hs.AddHost(c)
	}
	return hs
}

func uploadmetafile(f *os.File, minShards int, contracts renter.ContractSet, metaPath string) error {
	stat, err := f.Stat()
	if err != nil {
		return errors.Wrap(err, "could not stat file")
	}

	c := makeSHARDClient()
	if synced, err := c.Synced(); !synced && err == nil {
		return errors.New("blockchain is not synchronized")
	}
	currentHeight, err := c.ChainHeight()
	if err != nil {
		return errors.Wrap(err, "could not determine current height")
	}

	dir, name := filepath.Dir(metaPath), strings.TrimSuffix(filepath.Base(metaPath), ".usa")
	fs := renterutil.NewFileSystem(dir, makeHostSet(contracts, c, currentHeight))
	defer fs.Close()
	pf, err := fs.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, stat.Mode(), minShards)
	if err != nil {
		return err
	}
	defer pf.Close()
	if err := trackUpload(pf, f); err != nil {
		return err
	}
	return fs.Close()
}

func uploadmetadir(dir, metaDir string, contracts renter.ContractSet, minShards int) error {
	c := makeSHARDClient()
	if synced, err := c.Synced(); !synced && err == nil {
		return errors.New("blockchain is not synchronized")
	}
	currentHeight, err := c.ChainHeight()
	if err != nil {
		return errors.Wrap(err, "could not determine current height")
	}
	fs := renterutil.NewFileSystem(metaDir, makeHostSet(contracts, c, currentHeight))
	defer fs.Close()

	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		fsPath, _ := filepath.Rel(dir, path)
		if info.IsDir() || err != nil {
			return fs.MkdirAll(fsPath, 0700)
		}
		pf, err := fs.Create(fsPath, minShards)
		if err != nil {
			return err
		}
		defer pf.Close()
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return trackUpload(pf, f)
	})
	if err != nil {
		return err
	}
	return fs.Close()
}

func resumeuploadmetafile(f *os.File, contracts renter.ContractSet, metaPath string) error {
	c := makeSHARDClient()
	if synced, err := c.Synced(); !synced && err == nil {
		return errors.New("blockchain is not synchronized")
	}
	currentHeight, err := c.ChainHeight()
	if err != nil {
		return errors.Wrap(err, "could not determine current height")
	}

	dir, name := filepath.Dir(metaPath), strings.TrimSuffix(filepath.Base(metaPath), ".usa")
	fs := renterutil.NewFileSystem(dir, makeHostSet(contracts, c, currentHeight))
	defer fs.Close()
	pf, err := fs.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0, 0)
	if err != nil {
		return err
	}
	defer pf.Close()
	stat, _ := pf.Stat()
	if _, err := f.Seek(stat.Size(), io.SeekStart); err != nil {
		return err
	}
	if err := trackUpload(pf, f); err != nil {
		return err
	}
	return fs.Close()
}

func resumedownload(f *os.File, metaPath string, pf *renterutil.PseudoFile) error {
	if ok, err := renter.MetaFileCanDownload(metaPath); err == nil && !ok {
		return errors.New("file is not sufficiently uploaded")
	}
	// set file mode and size
	stat, err := f.Stat()
	if err != nil {
		return errors.Wrap(err, "could not stat file")
	}
	pstat, err := pf.Stat()
	if err != nil {
		return err
	}
	if stat.Mode() != pstat.Mode() {
		if err := f.Chmod(pstat.Mode()); err != nil {
			return errors.Wrap(err, "could not set file mode")
		}
	}
	if stat.Size() > pstat.Size() {
		if err := f.Truncate(pstat.Size()); err != nil {
			return errors.Wrap(err, "could not resize file")
		}
	}
	// resume at end of file
	offset := stat.Size()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	if _, err := pf.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	return trackDownload(f, pf, offset)
}

func downloadmetafile(f *os.File, contracts renter.ContractSet, metaPath string) error {
	if ok, err := renter.MetaFileCanDownload(metaPath); err == nil && !ok {
		return errors.New("file is not sufficiently uploaded")
	}

	dir, name := filepath.Dir(metaPath), strings.TrimSuffix(filepath.Base(metaPath), ".usa")
	fs := renterutil.NewFileSystem(dir, makeHostSet(contracts, makeSHARDClient(), 0))
	defer fs.Close()
	pf, err := fs.Open(name)
	if err != nil {
		return err
	}
	defer pf.Close()
	if err := resumedownload(f, metaPath, pf); err != nil {
		return err
	}
	return fs.Close()
}

func downloadmetastream(w io.Writer, contracts renter.ContractSet, metaPath string) error {
	if ok, err := renter.MetaFileCanDownload(metaPath); err == nil && !ok {
		return errors.New("file is not sufficiently uploaded")
	}

	dir, name := filepath.Dir(metaPath), strings.TrimSuffix(filepath.Base(metaPath), ".usa")
	fs := renterutil.NewFileSystem(dir, makeHostSet(contracts, makeSHARDClient(), 0))
	defer fs.Close()
	pf, err := fs.Open(name)
	if err != nil {
		return err
	}
	defer pf.Close()
	stat, err := pf.Stat()
	if err != nil {
		return err
	} else if stat.IsDir() {
		return errors.New("is a directory")
	}
	index := stat.Sys().(renter.MetaIndex)

	buf := make([]byte, renterhost.SectorSize*index.MinShards)
	_, err = io.CopyBuffer(w, pf, buf)
	if err != nil {
		return err
	}
	return fs.Close()
}

func downloadmetadir(dir string, contracts renter.ContractSet, metaDir string) error {
	fs := renterutil.NewFileSystem(metaDir, makeHostSet(contracts, makeSHARDClient(), 0))
	defer fs.Close()

	err := filepath.Walk(metaDir, func(metaPath string, info os.FileInfo, err error) error {
		if info.IsDir() || err != nil {
			return nil
		}
		name := strings.TrimSuffix(strings.TrimPrefix(metaPath, metaDir), ".usa")
		pf, err := fs.Open(name)
		if err != nil {
			return err
		}
		defer pf.Close()
		fpath := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(fpath), 0700)
		f, err := os.OpenFile(fpath, os.O_RDWR|os.O_CREATE, info.Mode())
		if err != nil {
			return err
		}
		return resumedownload(f, metaPath, pf)
	})
	if err != nil {
		return err
	}
	return fs.Close()
}

func checkupMeta(contracts renter.ContractSet, metaPath string) error {
	m, err := renter.ReadMetaFile(metaPath)
	if err != nil {
		return errors.Wrap(err, "could not load metafile")
	}

	c := makeSHARDClient()
	for r := range renterutil.Checkup(contracts, m, c) {
		if r.Error != nil {
			fmt.Printf("FAIL Host %v:\n\t%v\n", r.Host.ShortKey(), r.Error)
		} else {
			fmt.Printf("OK   Host %v: Latency %0.3fms, Bandwidth %0.3f Mbps\n",
				r.Host.ShortKey(), r.Latency.Seconds()*1000, r.Bandwidth)
		}
	}

	return nil
}

func migrateFile(f *os.File, contracts renter.ContractSet, metaPath string) error {
	m, err := renter.ReadMetaFile(metaPath)
	if err != nil {
		return errors.Wrap(err, "could not load metafile")
	}

	c := makeSHARDClient()
	if synced, err := c.Synced(); !synced && err == nil {
		return errors.New("blockchain is not synchronized")
	}
	currentHeight, err := c.ChainHeight()
	if err != nil {
		return errors.Wrap(err, "could not determine current height")
	}
	op := renterutil.MigrateFile(f, contracts, m, c, currentHeight)
	return trackMigrateFile(metaPath, op)
}

func migrateDirFile(dir string, contracts renter.ContractSet, metaDir string) error {
	c := makeSHARDClient()
	if synced, err := c.Synced(); !synced && err == nil {
		return errors.New("blockchain is not synchronized")
	}
	currentHeight, err := c.ChainHeight()
	if err != nil {
		return errors.Wrap(err, "could not determine current height")
	}
	metafileIter := renterutil.NewRecursiveMetaFileIter(metaDir, dir)
	op := renterutil.MigrateDirFile(contracts, metafileIter, c, currentHeight)
	return trackMigrateDir(op)
}

func migrateRemote(contracts renter.ContractSet, metaPath string) error {
	m, err := renter.ReadMetaFile(metaPath)
	if err != nil {
		return errors.Wrap(err, "could not load metafile")
	}

	c := makeSHARDClient()
	if synced, err := c.Synced(); !synced && err == nil {
		return errors.New("blockchain is not synchronized")
	}
	currentHeight, err := c.ChainHeight()
	if err != nil {
		return errors.Wrap(err, "could not determine current height")
	}
	op := renterutil.MigrateRemote(contracts, m, c, currentHeight)
	return trackMigrateFile(metaPath, op)
}

func migrateDirRemote(contracts renter.ContractSet, metaDir string) error {
	c := makeSHARDClient()
	if synced, err := c.Synced(); !synced && err == nil {
		return errors.New("blockchain is not synchronized")
	}
	currentHeight, err := c.ChainHeight()
	if err != nil {
		return errors.Wrap(err, "could not determine current height")
	}
	fileIter := renterutil.NewRecursiveMigrateDirIter(metaDir)
	op := renterutil.MigrateDirRemote(contracts, fileIter, c, currentHeight)
	return trackMigrateDir(op)
}
