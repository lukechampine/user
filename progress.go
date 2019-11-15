package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh/terminal"
	"lukechampine.com/us/renter"
	"lukechampine.com/us/renter/renterutil"
	"lukechampine.com/us/renterhost"
)

type trackWriter struct {
	w                io.Writer
	name             string
	off, xfer, total int64
	start            time.Time
	sigChan          <-chan os.Signal
}

func (tw *trackWriter) Write(p []byte) (int, error) {
	// check for cancellation
	select {
	case <-tw.sigChan:
		return 0, context.Canceled
	default:
	}
	n, err := tw.w.Write(p)
	tw.xfer += int64(n)
	printSimpleProgress(tw.name, tw.off, tw.xfer, tw.total, time.Since(tw.start))
	return n, err
}

func trackDownload(f *os.File, pf *renterutil.PseudoFile, off int64) error {
	stat, err := pf.Stat()
	if err != nil {
		return err
	}
	if off == stat.Size() {
		printAlreadyFinished(f.Name(), off)
		fmt.Println()
		return nil
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGPIPE)
	tw := &trackWriter{
		w:       f,
		name:    f.Name(),
		off:     off,
		total:   stat.Size(),
		start:   time.Now(),
		sigChan: sigChan,
	}
	index := stat.Sys().(renter.MetaIndex)
	buf := make([]byte, renterhost.SectorSize*index.MinShards)
	_, err = io.CopyBuffer(tw, pf, buf)
	if err == context.Canceled {
		err = nil
	}
	fmt.Println()
	return err
}

// Writes to PseudoFiles are buffered. Wrap the Write method so that we Sync
// after each Write. Otherwise, our transfer speeds will reflect the buffer
// speed, not the actual upload speed. (However, Syncing after each Write means
// that if we don't buffer the Writes ourselves, we'll waste a lot of data.)
type syncWriter struct {
	pf *renterutil.PseudoFile
}

func (sw syncWriter) Write(p []byte) (int, error) {
	n, err := sw.pf.Write(p)
	if err == nil {
		err = sw.pf.Sync()
	}
	return n, err
}

func trackUpload(pf *renterutil.PseudoFile, f *os.File, sync bool) error {
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	pstat, err := pf.Stat()
	if err != nil {
		return err
	}
	if pstat.Size() == stat.Size() {
		printAlreadyFinished(f.Name(), pstat.Size())
		fmt.Println()
		return nil
	}

	var w io.Writer = pf
	if sync {
		w = syncWriter{pf}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGPIPE)
	tw := &trackWriter{
		w:       w,
		name:    f.Name(),
		off:     pstat.Size(),
		total:   stat.Size(),
		start:   time.Now(),
		sigChan: sigChan,
	}
	// print initial progress
	printSimpleProgress(f.Name(), tw.off, tw.xfer, tw.total, time.Since(tw.start))
	// start transfer
	index := pstat.Sys().(renter.MetaIndex)
	buf := make([]byte, renterhost.SectorSize*index.MinShards)
	_, err = io.CopyBuffer(tw, f, buf)
	if err == context.Canceled {
		err = nil
	}
	fmt.Println()
	return err
}

type trackReader struct {
	r                io.Reader
	name             string
	off, xfer, total int64
	start            time.Time
	sigChan          <-chan os.Signal
}

func (tr *trackReader) Read(p []byte) (int, error) {
	// check for cancellation
	select {
	case <-tr.sigChan:
		return 0, context.Canceled
	default:
	}
	n, err := tr.r.Read(p)
	tr.xfer += int64(n)
	printSimpleProgress(tr.name, tr.off, tr.xfer, tr.total, time.Since(tr.start))
	return n, err
}

func trackMigrate(migrator *renterutil.Migrator, metaPath string, src io.Reader) error {
	m, err := renter.ReadMetaFile(metaPath)
	if err != nil {
		return errors.Wrap(err, "could not load metafile")
	}
	if !migrator.NeedsMigrate(m) {
		printAlreadyFinished(metaPath, m.Filesize)
		fmt.Println()
		return nil
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGPIPE)
	tr := &trackReader{
		r:       src,
		name:    metaPath,
		off:     0,
		total:   m.Filesize,
		start:   time.Now(),
		sigChan: sigChan,
	}
	err = migrator.AddFile(m, tr, func(newM *renter.MetaFile) error {
		printSimpleProgress(tr.name, tr.off, tr.total, tr.total, time.Since(tr.start))
		return renter.WriteMetaFile(metaPath, newM)
	})
	if err == context.Canceled {
		return nil
	} else if err != nil {
		return err
	}
	err = migrator.Flush()
	fmt.Println()
	return err
}

// progress bar helpers

func formatFilename(name string, maxLen int) string {
	//name = filepath.Base(name)
	if len(name) > maxLen {
		name = name[:maxLen]
	}
	return name
}

func getWidth() int {
	termWidth, _, err := terminal.GetSize(0)
	if err != nil {
		return 80 // sane default
	}
	return termWidth
}

func makeBuf(width int) []rune {
	buf := make([]rune, width)
	for i := range buf {
		buf[i] = ' '
	}
	return buf
}

func printSimpleProgress(filename string, start, xfer, total int64, elapsed time.Duration) {
	// prevent divide-by-zero
	if elapsed == 0 {
		elapsed = 1
	}
	termWidth := getWidth()
	bytesPerSec := int64(float64(xfer) / elapsed.Seconds())
	pct := (100 * (start + xfer)) / total
	metrics := fmt.Sprintf("%4v%%   %8s  %9s/s    ", pct, filesizeUnits(total), filesizeUnits(bytesPerSec))
	name := formatFilename(filename, termWidth-len(metrics)-4)
	buf := makeBuf(termWidth)
	copy(buf, []rune(name))
	copy(buf[len(buf)-len(metrics):], []rune(metrics))
	fmt.Printf("\r%s", string(buf))
}

func printAlreadyFinished(filename string, total int64) {
	termWidth := getWidth()
	metrics := fmt.Sprintf("%4v%%   %8s  %9s/s    ", 100.0, filesizeUnits(total), "--- B")
	name := formatFilename(filename, termWidth-len(metrics)-4)
	buf := makeBuf(termWidth)
	copy(buf, []rune(name))
	copy(buf[len(buf)-len(metrics):], []rune(metrics))
	fmt.Printf("\r%s", string(buf))
}
