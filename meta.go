package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
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

func uploadmetafile(f *os.File, minShards int, hosts *renterutil.HostSet, metaPath string) error {
	stat, err := f.Stat()
	if err != nil {
		return errors.Wrap(err, "could not stat file")
	}

	dir, name := filepath.Dir(metaPath), strings.TrimSuffix(filepath.Base(metaPath), ".usa")
	fs := renterutil.NewFileSystem(dir, hosts)
	defer fs.Close()
	pf, err := fs.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, stat.Mode(), minShards)
	if err != nil {
		return err
	}
	defer pf.Close()
	if err := trackUpload(pf, f, true); err != nil {
		return err
	}
	return fs.Close()
}

func uploadmetadir(dir, metaDir string, hosts *renterutil.HostSet, minShards int) error {
	fs := renterutil.NewFileSystem(metaDir, hosts)
	defer fs.Close()

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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
		return trackUpload(pf, f, false)
	})
	if err != nil {
		return err
	}
	return fs.Close()
}

func resumeuploadmetafile(f *os.File, hosts *renterutil.HostSet, metaPath string) error {
	dir, name := filepath.Dir(metaPath), strings.TrimSuffix(filepath.Base(metaPath), ".usa")
	fs := renterutil.NewFileSystem(dir, hosts)
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
	if err := trackUpload(pf, f, true); err != nil {
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

func downloadmetafile(f *os.File, hosts *renterutil.HostSet, metaPath string) error {
	if ok, err := renter.MetaFileCanDownload(metaPath); err == nil && !ok {
		return errors.New("file is not sufficiently uploaded")
	}

	dir, name := filepath.Dir(metaPath), strings.TrimSuffix(filepath.Base(metaPath), ".usa")
	fs := renterutil.NewFileSystem(dir, hosts)
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

func downloadmetastream(w io.Writer, hosts *renterutil.HostSet, metaPath string) error {
	if ok, err := renter.MetaFileCanDownload(metaPath); err == nil && !ok {
		return errors.New("file is not sufficiently uploaded")
	}

	dir, name := filepath.Dir(metaPath), strings.TrimSuffix(filepath.Base(metaPath), ".usa")
	fs := renterutil.NewFileSystem(dir, hosts)
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

func downloadmetadir(dir string, hosts *renterutil.HostSet, metaDir string) error {
	fs := renterutil.NewFileSystem(metaDir, hosts)
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

func checkupMeta(contracts renter.ContractSet, hkr renter.HostKeyResolver, metaPath string) error {
	m, err := renter.ReadMetaFile(metaPath)
	if err != nil {
		return errors.Wrap(err, "could not load metafile")
	}

	for r := range renterutil.Checkup(contracts, m, hkr) {
		if r.Error != nil {
			fmt.Printf("FAIL Host %v:\n\t%v\n", r.Host.ShortKey(), r.Error)
		} else {
			fmt.Printf("OK   Host %v: Latency %0.3fms, Bandwidth %0.3f Mbps\n",
				r.Host.ShortKey(), r.Latency.Seconds()*1000, r.Bandwidth)
		}
	}

	return nil
}

func migrateLocal(f *os.File, hosts *renterutil.HostSet, metaPath string) error {
	defer hosts.Close()
	migrator := renterutil.NewMigrator(hosts)
	return trackMigrate(migrator, metaPath, f)
}

func migrateDirLocal(dir string, hosts *renterutil.HostSet, metaDir string) error {
	defer hosts.Close()
	migrator := renterutil.NewMigrator(hosts)

	err := filepath.Walk(metaDir, func(metaPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		} else if info.IsDir() {
			return nil
		}
		name, _ := filepath.Rel(metaDir, metaPath)
		filePath := strings.TrimSuffix(filepath.Join(dir, name), metafileExt)

		f, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer f.Close()
		return trackMigrate(migrator, metaPath, f)
	})
	if err != nil {
		return err
	}
	if err := migrator.Flush(); err != nil {
		return err
	}
	return nil
}

func migrateRemote(hosts *renterutil.HostSet, metaPath string) error {
	defer hosts.Close()

	dir, name := filepath.Dir(metaPath), strings.TrimSuffix(filepath.Base(metaPath), ".usa")
	fs := renterutil.NewFileSystem(dir, hosts)
	defer fs.Close()
	pf, err := fs.Open(name)
	if err != nil {
		return err
	}

	migrator := renterutil.NewMigrator(hosts)
	return trackMigrate(migrator, metaPath, pf)
}

func migrateDirRemote(hosts *renterutil.HostSet, metaDir string) error {
	defer hosts.Close()

	fs := renterutil.NewFileSystem(metaDir, hosts)
	defer fs.Close()
	migrator := renterutil.NewMigrator(hosts)

	err := filepath.Walk(metaDir, func(metaPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		} else if info.IsDir() {
			return nil
		}
		fsPath, _ := filepath.Rel(metaDir, metaPath)
		pf, err := fs.Open(strings.TrimSuffix(fsPath, ".usa"))
		if err != nil {
			return errors.Wrap(err, "could not open metafile for reading")
		}
		defer pf.Close()
		return trackMigrate(migrator, metaPath, pf)
	})
	if err != nil {
		return err
	}
	if err := migrator.Flush(); err != nil {
		return err
	}
	return nil
}
