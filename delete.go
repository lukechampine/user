package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/errors"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"lukechampine.com/us/hostdb"
	"lukechampine.com/us/renter"
	"lukechampine.com/us/renter/proto"
	"lukechampine.com/us/renterhost"
)

func deleteUnreferencedSectors(contracts renter.ContractSet, hkr renter.HostKeyResolver, metaDir string) error {
	currentHeight, err := getCurrentHeight()
	if err != nil {
		return errors.Wrap(err, "could not get current height")
	}

	// build a set of all sector roots stored on hosts
	hosts := make(map[hostdb.HostPublicKey]map[crypto.Hash]uint64)
	for _, contract := range contracts {
		err := func() error {
			hostIP, err := hkr.ResolveHostKey(contract.HostKey)
			if err != nil {
				return err
			}
			s, err := proto.NewSession(hostIP, contract.HostKey, contract.ID, contract.RenterKey, currentHeight)
			if err != nil {
				return err
			}
			defer s.Close()
			roots, err := s.SectorRoots(0, s.Revision().NumSectors())
			if err != nil {
				return err
			}
			rootMap := make(map[crypto.Hash]uint64, len(roots))
			for i := range roots {
				rootMap[roots[i]] = uint64(i)
			}
			hosts[contract.HostKey] = rootMap
			return nil
		}()
		if err != nil {
			fmt.Printf("Could not download sector roots from host %v: %v", contract.HostKey.ShortKey(), err)
		}
	}

	var origSectors int
	for _, roots := range hosts {
		origSectors += len(roots)
	}

	// remove the roots of each file from the set
	var numFiles int
	var fileSectors int
	err = filepath.Walk(metaDir, func(path string, info os.FileInfo, _ error) error {
		if info.IsDir() || !strings.HasSuffix(path, ".usa") {
			return nil
		}
		m, err := renter.ReadMetaFile(path)
		if err != nil {
			// don't continue if a file couldn't be read; the user needs to be
			// confident that all files were checked
			return err
		}
		for i := range m.Hosts {
			fileSectors += len(m.Shards[i]) // TODO: need to deduplicate this count
			roots, ok := hosts[m.Hosts[i]]
			if !ok {
				continue
			}
			for _, ss := range m.Shards[i] {
				delete(roots, ss.MerkleRoot)
			}
		}
		numFiles++
		return nil
	})
	if err != nil {
		return err
	}

	var garbage int
	for _, roots := range hosts {
		garbage += len(roots)
	}
	if garbage == 0 {
		fmt.Println("No unreferenced sectors found.")
		return nil
	}

	fmt.Printf(`
Cross-referenced %v sectors in %v metafiles with %v sectors stored on %v hosts.
%v unreferenced sectors (%v) will be deleted.
Press ENTER to proceed, or Ctrl-C to abort.
`, fileSectors, numFiles, origSectors, len(hosts),
		garbage, filesizeUnits(int64(garbage*renterhost.SectorSize)))
	fmt.Scanln()

	// delete from each host
	//
	// TODO: parallelize
	for _, contract := range contracts {
		roots, ok := hosts[contract.HostKey]
		if !ok {
			continue // must be one of the hosts that failed earlier
		}
		if len(roots) == 0 {
			fmt.Printf("%v: Nothing to delete", contract.HostKey.ShortKey())
			continue
		}
		err := deleteFromHost(hkr, contract, roots)
		if err != nil {
			fmt.Printf("%v: Deletion failed: %v\n", contract.HostKey.ShortKey(), err)
		} else {
			fmt.Printf("%v: Deleted %v sectors\n", contract.HostKey.ShortKey(), len(roots))
		}
	}
	return nil
}

func deleteFromHost(hkr renter.HostKeyResolver, contract renter.Contract, roots map[crypto.Hash]uint64) error {
	// connect to host
	hostIP, err := hkr.ResolveHostKey(contract.HostKey)
	if err != nil {
		return err
	}
	currentHeight, err := getCurrentHeight()
	if err != nil {
		return err
	}
	s, err := proto.NewSession(hostIP, contract.HostKey, contract.ID, contract.RenterKey, currentHeight)
	if err != nil {
		return err
	}
	defer s.Close()

	// The Write RPC supports "swap(i,j)" and "trim(i)" (deleting i sectors
	// from the end). So we need to swap all the "bad" sectors to the end in
	// order to delete them with a subsequent trim.

	// first, extract the indices of each "bad" sector
	badIndices := make([]int, 0, len(roots))
	for _, index := range roots {
		badIndices = append(badIndices, int(index))
	}
	// sort in descending order so that we can use 'range'
	sort.Sort(sort.Reverse(sort.IntSlice(badIndices)))

	// iterate backwards from the end of the contract, swapping each "good"
	// sector with one of the "bad" sectors.
	var actions []renterhost.RPCWriteAction
	cIndex := s.Revision().NumSectors() - 1
	for _, rIndex := range badIndices {
		if cIndex != rIndex {
			// swap a "good" sector for a "bad" sector
			actions = append(actions, renterhost.RPCWriteAction{
				Type: renterhost.RPCWriteActionSwap,
				A:    uint64(cIndex),
				B:    uint64(rIndex),
			})
		}
		cIndex--
	}
	// trim all "bad" sectors
	actions = append(actions, renterhost.RPCWriteAction{
		Type: renterhost.RPCWriteActionTrim,
		A:    uint64(len(badIndices)),
	})

	// request the swap+delete operation
	//
	// NOTE: siad hosts will accept up to 20 MiB of data in the request,
	// which should be sufficient to delete up to 2.5 TiB of sector data
	// at a time.
	return s.Write(actions)
}
