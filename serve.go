package main

import (
	"bufio"
	"log"
	"net/http"
	"os"
	"os/signal"

	"lukechampine.com/us/renter/renterutil"
)

func serve(hosts *renterutil.HostSet, metaDir, addr string) error {
	pfs := renterutil.NewFileSystem(metaDir, hosts)
	srv := &http.Server{
		Addr:    addr,
		Handler: http.FileServer(&httpFS{pfs}),
	}
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)
		<-sigChan
		log.Println("Stopping server...")
		srv.Close()
		pfs.Close()
	}()
	log.Printf("Listening on %v...", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// A bufferedFile wraps a renterutil.PseudoFile in a bufio.Reader for better
// performance.
type bufferedFile struct {
	*renterutil.PseudoFile
	br *bufio.Reader
}

func (f *bufferedFile) Read(p []byte) (int, error) {
	if f.br == nil {
		f.br = bufio.NewReaderSize(f.PseudoFile, 1<<22) // 4 MiB
	}
	return f.br.Read(p)
}

func (f *bufferedFile) Seek(offset int64, whence int) (int64, error) {
	n, err := f.PseudoFile.Seek(offset, whence)
	if f.br != nil {
		f.br.Reset(f.PseudoFile)
	}
	return n, err
}

type httpFS struct {
	pfs *renterutil.PseudoFS
}

func (hfs *httpFS) Open(name string) (http.File, error) {
	pf, err := hfs.pfs.Open(name)
	if err != nil {
		return nil, err
	}
	return &bufferedFile{
		PseudoFile: pf,
	}, nil
}
